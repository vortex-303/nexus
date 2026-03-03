package server

import (
	"log"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
)

type createWorkspaceReq struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Password    string `json:"password,omitempty"`
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
		slug, "", userID,
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
	if accountID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role, account_id) VALUES (?, ?, ?, ?)",
			userID, req.DisplayName, "admin", accountID,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role) VALUES (?, ?, ?)",
			userID, req.DisplayName, "admin",
		)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	// Add Brain as first member and create definition files
	if err := s.ensureBrainMember(slug); err != nil {
		log.Printf("[brain] failed to create brain member for %s: %v", slug, err)
	}
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	if err := brain.EnsureDefaults(brainDir); err != nil {
		log.Printf("[brain] failed to create definition files for %s: %v", slug, err)
	}
	if err := brain.EnsureDefaultSkills(brainDir); err != nil {
		log.Printf("[brain] failed to create default skills for %s: %v", slug, err)
	}
	if err := s.ensureBuiltinAgents(slug); err != nil {
		log.Printf("[builtin] failed to seed built-in agents for %s: %v", slug, err)
	}

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
	_, err = wdb.DB.Exec(
		"INSERT INTO members (id, display_name, role) VALUES (?, ?, ?)",
		userID, req.DisplayName, "member",
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
}

// handleCreateInvite generates an invite link for the workspace. Admin only.
func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

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

	writeJSON(w, http.StatusCreated, createInviteResp{
		InviteToken: inviteToken,
		InviteURL:   inviteURL,
	})
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

	rows, err := wdb.DB.Query("SELECT id, display_name, role FROM members ORDER BY joined_at")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	defer rows.Close()

	var members []memberResp
	for rows.Next() {
		var m memberResp
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Role); err != nil {
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
