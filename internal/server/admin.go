package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

// logAdminAction records a superadmin action to the audit log.
func (s *Server) logAdminAction(claims *auth.Claims, action, targetType, targetID, detail string) {
	email := ""
	if claims.AccountID != "" {
		_ = s.global.DB.QueryRow("SELECT email FROM accounts WHERE id = ?", claims.AccountID).Scan(&email)
	}
	s.global.DB.Exec(
		"INSERT INTO admin_audit_log (id, actor_id, actor_email, action, target_type, target_id, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id.New(), claims.AccountID, email, action, targetType, targetID, detail,
	)
}

// handleAdminStats returns platform-wide statistics.
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	var totalAccounts, totalWorkspaces int
	s.global.DB.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&totalAccounts)
	s.global.DB.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&totalWorkspaces)

	var suspendedWorkspaces int
	s.global.DB.QueryRow("SELECT COUNT(*) FROM workspaces WHERE suspended = TRUE").Scan(&suspendedWorkspaces)

	var bannedAccounts int
	s.global.DB.QueryRow("SELECT COUNT(*) FROM accounts WHERE banned = TRUE").Scan(&bannedAccounts)

	// Count total members + messages across all workspaces
	var totalMembers, totalMessages, totalFiles int64
	rows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug string
			rows.Scan(&slug)
			wdb, err := s.ws.Open(slug)
			if err != nil {
				continue
			}
			var mc, msgc, fc int64
			wdb.DB.QueryRow("SELECT COUNT(*) FROM members").Scan(&mc)
			wdb.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgc)
			wdb.DB.QueryRow("SELECT COUNT(*) FROM files").Scan(&fc)
			totalMembers += mc
			totalMessages += msgc
			totalFiles += fc
		}
	}

	// Disk usage of data dir
	var diskBytes int64
	filepath.Walk(s.cfg.DataDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			diskBytes += info.Size()
		}
		return nil
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"accounts":             totalAccounts,
		"workspaces":           totalWorkspaces,
		"suspended_workspaces": suspendedWorkspaces,
		"banned_accounts":      bannedAccounts,
		"total_members":        totalMembers,
		"total_messages":       totalMessages,
		"total_files":          totalFiles,
		"disk_bytes":           diskBytes,
	})
}

type adminWorkspace struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	CreatedBy       string `json:"created_by"`
	CreatedAt       string `json:"created_at"`
	Suspended       bool   `json:"suspended"`
	SuspendedReason string `json:"suspended_reason,omitempty"`
	MemberCount     int    `json:"member_count"`
	MessageCount    int    `json:"message_count"`
}

// handleAdminListWorkspaces returns all workspaces with stats.
func (s *Server) handleAdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.global.DB.Query("SELECT slug, name, COALESCE(created_by, ''), created_at, suspended, suspended_reason FROM workspaces ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}
	defer rows.Close()

	var result []adminWorkspace
	for rows.Next() {
		var ws adminWorkspace
		if err := rows.Scan(&ws.Slug, &ws.Name, &ws.CreatedBy, &ws.CreatedAt, &ws.Suspended, &ws.SuspendedReason); err != nil {
			continue
		}
		wdb, err := s.ws.Open(ws.Slug)
		if err == nil {
			wdb.DB.QueryRow("SELECT COUNT(*) FROM members").Scan(&ws.MemberCount)
			wdb.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&ws.MessageCount)
		}
		result = append(result, ws)
	}

	if result == nil {
		result = []adminWorkspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": result})
}

type adminAccount struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	IsSuperadmin bool   `json:"is_superadmin"`
	Banned       bool   `json:"banned"`
	CreatedAt    string `json:"created_at"`
}

// handleAdminListAccounts returns all registered accounts.
func (s *Server) handleAdminListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.global.DB.Query("SELECT id, COALESCE(email, ''), display_name, is_superadmin, banned, created_at FROM accounts ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}
	defer rows.Close()

	var result []adminAccount
	for rows.Next() {
		var a adminAccount
		if err := rows.Scan(&a.ID, &a.Email, &a.DisplayName, &a.IsSuperadmin, &a.Banned, &a.CreatedAt); err != nil {
			continue
		}
		result = append(result, a)
	}

	if result == nil {
		result = []adminAccount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": result})
}

// handleAdminSuspendWorkspace toggles workspace suspension.
func (s *Server) handleAdminSuspendWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	var req struct {
		Suspended bool   `json:"suspended"`
		Reason    string `json:"reason"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	res, err := s.global.DB.Exec("UPDATE workspaces SET suspended = ?, suspended_reason = ? WHERE slug = ?", req.Suspended, req.Reason, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workspace")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	action := "workspace.unsuspend"
	if req.Suspended {
		action = "workspace.suspend"
	}
	s.logAdminAction(claims, action, "workspace", slug, req.Reason)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminBanAccount toggles account ban.
func (s *Server) handleAdminBanAccount(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountID")
	claims := auth.GetClaims(r)

	var req struct {
		Banned bool `json:"banned"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Prevent banning yourself
	if accountID == claims.AccountID {
		writeError(w, http.StatusBadRequest, "cannot ban yourself")
		return
	}

	// Prevent banning other superadmins
	var targetSA bool
	s.global.DB.QueryRow("SELECT is_superadmin FROM accounts WHERE id = ?", accountID).Scan(&targetSA)
	if targetSA {
		writeError(w, http.StatusForbidden, "cannot ban a superadmin")
		return
	}

	res, err := s.global.DB.Exec("UPDATE accounts SET banned = ? WHERE id = ?", req.Banned, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	action := "account.unban"
	if req.Banned {
		action = "account.ban"
	}
	s.logAdminAction(claims, action, "account", accountID, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminImpersonate generates a workspace-scoped token for any member.
func (s *Server) handleAdminImpersonate(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)

	var req struct {
		WorkspaceSlug string `json:"workspace_slug"`
		MemberID      string `json:"member_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(req.WorkspaceSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	var displayName, role string
	err = wdb.DB.QueryRow(
		"SELECT display_name, role FROM members WHERE id = ?", req.MemberID,
	).Scan(&displayName, &role)
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	token, err := s.jwt.Issue(req.MemberID, displayName, req.WorkspaceSlug, role, claims.AccountID, auth.WithSuperAdmin())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	s.logAdminAction(claims, "impersonate", "member", req.MemberID,
		fmt.Sprintf("workspace=%s, as=%s", req.WorkspaceSlug, displayName))

	writeJSON(w, http.StatusOK, map[string]string{"token": token, "slug": req.WorkspaceSlug})
}

// handleAdminEnterWorkspace generates a token to enter any workspace as superadmin observer.
func (s *Server) handleAdminEnterWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	// Check workspace exists
	var name string
	err := s.global.DB.QueryRow("SELECT name FROM workspaces WHERE slug = ?", slug).Scan(&name)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	// Check if superadmin already has a member record in this workspace
	var memberID, memberRole string
	err = wdb.DB.QueryRow("SELECT id, role FROM members WHERE account_id = ?", claims.AccountID).Scan(&memberID, &memberRole)
	if err != nil {
		// Create a temporary admin member record for the superadmin
		memberID = id.New()
		memberRole = "admin"
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role, account_id) VALUES (?, ?, ?, ?)",
			memberID, claims.DisplayName, "admin", claims.AccountID,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to join workspace")
			return
		}
	}

	token, err := s.jwt.Issue(memberID, claims.DisplayName, slug, memberRole, claims.AccountID, auth.WithSuperAdmin())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	s.logAdminAction(claims, "enter_workspace", "workspace", slug, "")
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "slug": slug})
}

type auditEntry struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActorEmail string `json:"actor_email"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Detail     string `json:"detail"`
	CreatedAt  string `json:"created_at"`
}

// handleAdminAuditLog returns the admin audit log.
func (s *Server) handleAdminAuditLog(w http.ResponseWriter, r *http.Request) {
	rows, err := s.global.DB.Query("SELECT id, actor_id, actor_email, action, target_type, target_id, detail, created_at FROM admin_audit_log ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query audit log")
		return
	}
	defer rows.Close()

	var entries []auditEntry
	for rows.Next() {
		var e auditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &e.Detail, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	if entries == nil {
		entries = []auditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleAdminDeleteWorkspace soft-deletes a workspace.
func (s *Server) handleAdminDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	res, err := s.global.DB.Exec("DELETE FROM workspaces WHERE slug = ?", slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete workspace")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	s.logAdminAction(claims, "workspace.delete", "workspace", slug, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminWorkspaceDetail returns workspace metadata without private content.
func (s *Server) handleAdminWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var wsName, createdAt string
	err := s.global.DB.QueryRow("SELECT name, created_at FROM workspaces WHERE slug = ?", slug).Scan(&wsName, &createdAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	var memberCount, channelCount, totalMessages int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM members").Scan(&memberCount)
	wdb.DB.QueryRow("SELECT COUNT(*) FROM channels").Scan(&channelCount)
	wdb.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&totalMessages)

	// Members list (no content)
	type memberInfo struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		JoinedAt    string `json:"joined_at"`
	}
	var members []memberInfo
	mRows, err := wdb.DB.Query("SELECT id, display_name, role, joined_at FROM members ORDER BY joined_at")
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var m memberInfo
			if mRows.Scan(&m.ID, &m.DisplayName, &m.Role, &m.JoinedAt) == nil {
				members = append(members, m)
			}
		}
	}
	if members == nil {
		members = []memberInfo{}
	}

	// Channels list (no content)
	type channelInfo struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		MessageCount int    `json:"message_count"`
	}
	var channels []channelInfo
	cRows, err := wdb.DB.Query("SELECT c.id, c.name, c.type, (SELECT COUNT(*) FROM messages WHERE channel_id = c.id) FROM channels c WHERE c.archived = FALSE ORDER BY c.created_at")
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var c channelInfo
			if cRows.Scan(&c.ID, &c.Name, &c.Type, &c.MessageCount) == nil {
				channels = append(channels, c)
			}
		}
	}
	if channels == nil {
		channels = []channelInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":           slug,
		"name":           wsName,
		"created_at":     createdAt,
		"member_count":   memberCount,
		"channel_count":  channelCount,
		"total_messages": totalMessages,
		"members":        members,
		"channels":       channels,
	})
}

// handleAdminResetPassword resets an account's password.
func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountID")
	claims := auth.GetClaims(r)

	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new_password required")
		return
	}

	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	res, err := s.global.DB.Exec("UPDATE accounts SET password_hash = ? WHERE id = ?", string(hash), accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	s.logAdminAction(claims, "account.reset_password", "account", accountID, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminExportWorkspace exports workspace metadata as JSON download.
func (s *Server) handleAdminExportWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)

	var wsName, createdAt string
	err := s.global.DB.QueryRow("SELECT name, created_at FROM workspaces WHERE slug = ?", slug).Scan(&wsName, &createdAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open workspace")
		return
	}

	// Members
	type memberExport struct {
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		JoinedAt    string `json:"joined_at"`
	}
	var members []memberExport
	mRows, _ := wdb.DB.Query("SELECT display_name, role, joined_at FROM members ORDER BY joined_at")
	if mRows != nil {
		defer mRows.Close()
		for mRows.Next() {
			var m memberExport
			if mRows.Scan(&m.DisplayName, &m.Role, &m.JoinedAt) == nil {
				members = append(members, m)
			}
		}
	}

	// Channels
	type channelExport struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		MessageCount int    `json:"message_count"`
	}
	var channels []channelExport
	cRows, _ := wdb.DB.Query("SELECT c.name, c.type, (SELECT COUNT(*) FROM messages WHERE channel_id = c.id) FROM channels c WHERE c.archived = FALSE")
	if cRows != nil {
		defer cRows.Close()
		for cRows.Next() {
			var c channelExport
			if cRows.Scan(&c.Name, &c.Type, &c.MessageCount) == nil {
				channels = append(channels, c)
			}
		}
	}

	// Aggregate stats
	var totalMessages, totalTasks, totalDocs int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&totalMessages)
	wdb.DB.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&totalTasks)
	wdb.DB.QueryRow("SELECT COUNT(*) FROM documents").Scan(&totalDocs)

	export := map[string]any{
		"workspace":      map[string]string{"slug": slug, "name": wsName, "created_at": createdAt},
		"members":        members,
		"channels":       channels,
		"total_messages": totalMessages,
		"total_tasks":    totalTasks,
		"total_docs":     totalDocs,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-export.json"`, slug))
	json.NewEncoder(w).Encode(export)

	s.logAdminAction(claims, "workspace.export", "workspace", slug, "metadata only")
}

// handleAdminSetAnnouncement creates a platform announcement.
func (s *Server) handleAdminSetAnnouncement(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)

	var req struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if err := readJSON(r, &req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	if req.Type == "" {
		req.Type = "info"
	}

	// Deactivate previous announcements
	s.global.DB.Exec("UPDATE platform_announcements SET active = FALSE")

	announcementID := id.New()
	_, err := s.global.DB.Exec(
		"INSERT INTO platform_announcements (id, message, type, active, created_by) VALUES (?, ?, ?, TRUE, ?)",
		announcementID, req.Message, req.Type, claims.AccountID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create announcement")
		return
	}

	s.logAdminAction(claims, "announcement.set", "platform", announcementID, req.Message)
	writeJSON(w, http.StatusCreated, map[string]string{"id": announcementID})
}

// handleAdminClearAnnouncement deactivates all announcements.
func (s *Server) handleAdminClearAnnouncement(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	s.global.DB.Exec("UPDATE platform_announcements SET active = FALSE")
	s.logAdminAction(claims, "announcement.clear", "platform", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetAnnouncement returns the active platform announcement (public).
func (s *Server) handleGetAnnouncement(w http.ResponseWriter, r *http.Request) {
	var ann struct {
		ID        string `json:"id"`
		Message   string `json:"message"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
	}
	err := s.global.DB.QueryRow(
		"SELECT id, message, type, created_at FROM platform_announcements WHERE active = TRUE ORDER BY created_at DESC LIMIT 1",
	).Scan(&ann.ID, &ann.Message, &ann.Type, &ann.CreatedAt)
	if err != nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, ann)
}
