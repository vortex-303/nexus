package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// memberColorPalette is a 12-color palette for auto-assigning member colors.
var memberColorPalette = []string{
	"#3b82f6", // blue
	"#10b981", // emerald
	"#8b5cf6", // violet
	"#ef4444", // red
	"#f59e0b", // amber
	"#ec4899", // pink
	"#06b6d4", // cyan
	"#84cc16", // lime
	"#f97316", // orange
	"#6366f1", // indigo
	"#14b8a6", // teal
	"#e11d48", // rose
}

// assignMemberColor picks the next color from the palette based on member count.
func assignMemberColor(wdb *db.WorkspaceDB) string {
	var count int
	_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM members WHERE color != ''").Scan(&count)
	return memberColorPalette[count%len(memberColorPalette)]
}

// backfillMemberColors assigns colors to any member with an empty color.
func backfillMemberColors(wdb *db.WorkspaceDB) {
	rows, err := wdb.DB.Query("SELECT id FROM members WHERE color = '' ORDER BY joined_at")
	if err != nil {
		return
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	for _, memberID := range ids {
		color := assignMemberColor(wdb)
		_, _ = wdb.DB.Exec("UPDATE members SET color = ? WHERE id = ?", color, memberID)
	}
}

type createWorkspaceReq struct {
	DisplayName   string `json:"display_name"`
	WorkspaceName string `json:"workspace_name"`
	Email         string `json:"email,omitempty"`
	Password      string `json:"password,omitempty"`
}

type createWorkspaceResp struct {
	Slug  string `json:"slug"`
	Token string `json:"token"`
}

// handleCreateWorkspace creates a new workspace with a random slug.
// The creator becomes admin. No auth required — anonymous session created.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req createWorkspaceReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "Anonymous"
	}

	slug := id.Slug()
	userID := id.New()

	// If email+password provided, auto-register account
	var accountID string
	if req.Email != "" && req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		accountID = id.New()
		_, err = s.global.DB.Exec(
			"INSERT INTO accounts (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)",
			accountID, req.Email, string(hash), req.DisplayName,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
	}

	// Register workspace in global DB
	_, err := s.global.DB.Exec(
		"INSERT INTO workspaces (slug, name, created_by) VALUES (?, ?, ?)",
		slug, req.WorkspaceName, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workspace")
		return
	}

	// Open workspace DB (creates it + runs migrations)
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize workspace")
		return
	}

	// Create #general channel
	generalID := id.New()
	_, err = wdb.DB.Exec(
		"INSERT INTO channels (id, name, type, created_by) VALUES (?, ?, ?, ?)",
		generalID, "general", "public", userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create default channel")
		return
	}

	// Add creator as admin member
	creatorColor := assignMemberColor(wdb)
	if accountID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role, account_id, color) VALUES (?, ?, ?, ?, ?)",
			userID, req.DisplayName, "admin", accountID, creatorColor,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, ?, ?)",
			userID, req.DisplayName, "admin", creatorColor,
		)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	// Add Brain as first member and create definition files
	if err := s.ensureBrainMember(slug); err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("slug", slug).Msg("failed to create brain member")
	}
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	if err := brain.EnsureDefaults(brainDir); err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("slug", slug).Msg("failed to create definition files")
	}
	if err := s.ensureBuiltinAgents(slug); err != nil {
		logger.WithCategory(logger.CatAgent).Error().Err(err).Str("slug", slug).Msg("failed to seed built-in agents")
	}
	s.seedFreeMCPServers(slug)

	// Issue JWT
	token, err := s.jwt.Issue(userID, req.DisplayName, slug, "admin", accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusCreated, createWorkspaceResp{
		Slug:  slug,
		Token: token,
	})
}

type joinWorkspaceReq struct {
	DisplayName string `json:"display_name"`
	InviteToken string `json:"invite_token"`
}

type joinWorkspaceResp struct {
	Token    string `json:"token"`
	MemberID string `json:"member_id"`
}

// handleJoinWorkspace joins an existing workspace via invite token.
func (s *Server) handleJoinWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "missing workspace slug")
		return
	}

	var req joinWorkspaceReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "Anonymous"
	}

	// Verify invite token exists and is valid
	var wsSlug string
	err := s.global.DB.QueryRow(
		"SELECT workspace_slug FROM invite_tokens WHERE token = ? AND workspace_slug = ? AND (expires_at IS NULL OR expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))",
		req.InviteToken, slug,
	).Scan(&wsSlug)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid or expired invite")
		return
	}

	// Open workspace DB
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	// Add as member
	userID := id.New()
	joinColor := assignMemberColor(wdb)
	_, err = wdb.DB.Exec(
		"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, ?, ?)",
		userID, req.DisplayName, "member", joinColor,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	// Issue JWT
	token, err := s.jwt.Issue(userID, req.DisplayName, slug, "member", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, joinWorkspaceResp{Token: token, MemberID: userID})
}

type createInviteResp struct {
	InviteToken string `json:"invite_token"`
	InviteURL   string `json:"invite_url"`
	InviteCode  string `json:"invite_code"`
}

// handleCreateInvite generates an invite code + link for the workspace. Admin only.
func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	inviteToken := id.Short()
	inviteCode := id.InviteCode()
	expires := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02T15:04:05Z")

	_, err := s.global.DB.Exec(
		"INSERT INTO invite_tokens (token, workspace_slug, created_by, expires_at) VALUES (?, ?, ?, ?)",
		inviteToken, slug, claims.UserID, expires,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite")
		return
	}

	// Also store the short code pointing to the same workspace
	_, err = s.global.DB.Exec(
		"INSERT INTO invite_tokens (token, workspace_slug, created_by, expires_at) VALUES (?, ?, ?, ?)",
		inviteCode, slug, claims.UserID, expires,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite code")
		return
	}

	inviteURL := "/w/" + slug + "?invite=" + inviteToken
	if s.cfg.Domain != "" {
		inviteURL = "https://" + s.cfg.Domain + inviteURL
	}

	writeJSON(w, http.StatusCreated, createInviteResp{
		InviteToken: inviteToken,
		InviteURL:   inviteURL,
		InviteCode:  inviteCode,
	})
}

// handleJoinByCode joins a workspace using a short invite code (e.g. NX-A7B3).
// POST /api/join
func (s *Server) handleJoinByCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		DisplayName string `json:"display_name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	code := strings.TrimSpace(strings.ToUpper(req.Code))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "Anonymous"
	}

	// Look up the code
	var wsSlug string
	err := s.global.DB.QueryRow(
		"SELECT workspace_slug FROM invite_tokens WHERE token = ? AND (expires_at IS NULL OR expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))",
		code,
	).Scan(&wsSlug)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid or expired invite code")
		return
	}

	// Open workspace DB
	wdb, err := s.ws.Open(wsSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	// Add as member
	userID := id.New()
	joinColor := assignMemberColor(wdb)
	_, err = wdb.DB.Exec(
		"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, ?, ?)",
		userID, req.DisplayName, "member", joinColor,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	// Issue JWT
	token, err := s.jwt.Issue(userID, req.DisplayName, wsSlug, "member", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"member_id": userID,
		"slug":      wsSlug,
	})
}

// handleInviteByEmail sends an invite link via email.
func (s *Server) handleInviteByEmail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, "email required")
		return
	}

	// Check SMTP is configured
	host := s.getBrainSetting(slug, "email_outbound_host")
	if host == "" {
		writeError(w, http.StatusBadRequest, "SMTP not configured. Set up outbound email in Brain → Integrations.")
		return
	}

	// Create invite token
	inviteToken := id.Short()
	_, err := s.global.DB.Exec(
		"INSERT INTO invite_tokens (token, workspace_slug, created_by) VALUES (?, ?, ?)",
		inviteToken, slug, claims.UserID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite")
		return
	}

	inviteURL := "/w/" + slug + "?invite=" + inviteToken
	if s.cfg.Domain != "" {
		inviteURL = "https://" + s.cfg.Domain + inviteURL
	}

	// Get workspace name for email
	wsName := slug
	var displayName string
	row := s.global.DB.QueryRow("SELECT display_name FROM workspaces WHERE slug = ?", slug)
	if row.Scan(&displayName) == nil && displayName != "" {
		wsName = displayName
	}

	inviterName := claims.DisplayName
	if inviterName == "" {
		inviterName = "A team member"
	}

	subject := fmt.Sprintf("You're invited to join %s", wsName)
	body := fmt.Sprintf("%s has invited you to join the %s workspace.\n\nClick the link below to join:\n%s\n\nSee you there!", inviterName, wsName, inviteURL)

	s.sendOutboundEmail(slug, req.Email, subject, body, "")
	logger.WithCategory(logger.CatSystem).Info().Str("slug", slug).Str("email", req.Email).Str("inviter", claims.UserID).Msg("invite email sent")

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

type workspaceInfoResp struct {
	Slug    string       `json:"slug"`
	Name    string       `json:"name"`
	Members []memberResp `json:"members"`
}

type memberResp struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Color       string `json:"color"`
}

// handleGetWorkspace returns workspace info.
func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// Get workspace name + check suspension
	var name string
	var suspended bool
	err := s.global.DB.QueryRow("SELECT name, suspended FROM workspaces WHERE slug = ?", slug).Scan(&name, &suspended)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if suspended && !claims.SuperAdmin {
		writeError(w, http.StatusForbidden, "this workspace has been suspended")
		return
	}

	// Get members
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	// Backfill colors for members that don't have one yet
	backfillMemberColors(wdb)

	rows, err := wdb.DB.Query("SELECT id, display_name, role, color FROM members ORDER BY joined_at")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	defer rows.Close()

	var members []memberResp
	for rows.Next() {
		var m memberResp
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Role, &m.Color); err != nil {
			continue
		}
		members = append(members, m)
	}

	writeJSON(w, http.StatusOK, workspaceInfoResp{
		Slug:    slug,
		Name:    name,
		Members: members,
	})
}
