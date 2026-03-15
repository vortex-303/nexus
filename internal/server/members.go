package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/roles"
)

type updateRoleReq struct {
	MemberID string `json:"member_id"`
	Role     string `json:"role"`
}

// handleUpdateRole changes a member's role. Admin only.
func (s *Server) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req updateRoleReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !roles.IsValid(req.Role) {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	if req.MemberID == claims.UserID {
		writeError(w, http.StatusBadRequest, "cannot change your own role")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Verify target member exists
	var currentRole string
	err = wdb.DB.QueryRow("SELECT role FROM members WHERE id = ?", req.MemberID).Scan(&currentRole)
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	if currentRole == "agent" || currentRole == "brain" {
		writeError(w, http.StatusBadRequest, "cannot change role of agent or brain")
		return
	}

	// Update role
	_, err = wdb.DB.Exec("UPDATE members SET role = ? WHERE id = ?", req.Role, req.MemberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update role")
		return
	}

	// Clear overrides on role change (they inherit new defaults)
	checker := roles.NewChecker(wdb.DB)
	checker.ClearAllOverrides(req.MemberID)

	writeJSON(w, http.StatusOK, map[string]string{
		"member_id": req.MemberID,
		"role":      req.Role,
	})
}

type updatePermReq struct {
	MemberID   string `json:"member_id"`
	Permission string `json:"permission"`
	Granted    *bool  `json:"granted"` // null = clear override
}

// handleUpdatePermission sets or clears a permission override. Admin only.
func (s *Server) handleUpdatePermission(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req updatePermReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	perm := roles.Permission(req.Permission)
	valid := false
	for _, p := range roles.AllPermissions {
		if p == perm {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid permission")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	checker := roles.NewChecker(wdb.DB)

	if req.Granted == nil {
		// Clear override
		if err := checker.ClearOverride(req.MemberID, perm); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear override")
			return
		}
	} else {
		if err := checker.SetOverride(req.MemberID, perm, *req.Granted); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set override")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type memberDetailResp struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name"`
	Role         string          `json:"role"`
	Title        string          `json:"title"`
	Bio          string          `json:"bio"`
	Goals        string          `json:"goals"`
	Color        string          `json:"color"`
	JoinedAt     string          `json:"joined_at"`
	Permissions  map[string]bool `json:"permissions"`
	MessageCount int             `json:"message_count"`
	TaskCount    int             `json:"task_count"`
	LastActive   string          `json:"last_active"`
	IsOnline     bool            `json:"is_online"`
	// Agent-specific fields (only populated when role == "agent")
	Agent *agentProfileResp `json:"agent,omitempty"`
}

type agentProfileResp struct {
	Avatar          string `json:"avatar"`
	Description     string `json:"description"`
	Backstory       string `json:"backstory"`
	Model           string `json:"model"`
	TriggerType     string `json:"trigger_type"`
	IsActive        bool   `json:"is_active"`
	KnowledgeAccess bool   `json:"knowledge_access"`
	MemoryAccess    bool   `json:"memory_access"`
	CanDelegate     bool   `json:"can_delegate"`
	ToolCount       int    `json:"tool_count"`
	CreatedAt       string `json:"created_at"`
}

// handleGetMember returns a member's details including profile, stats, and effective permissions.
func (s *Server) handleGetMember(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	memberID := r.PathValue("memberID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var displayName, role, title, bio, goals, color, joinedAt string
	err = wdb.DB.QueryRow(
		"SELECT display_name, role, title, bio, goals, color, joined_at FROM members WHERE id = ?", memberID,
	).Scan(&displayName, &role, &title, &bio, &goals, &color, &joinedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	// Permissions
	checker := roles.NewChecker(wdb.DB)
	perms := checker.Resolve(memberID)
	permMap := make(map[string]bool, len(perms))
	for k, v := range perms {
		permMap[string(k)] = v
	}

	// Stats
	var msgCount, taskCount int
	var lastActive string
	_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE sender_id = ? AND deleted = FALSE", memberID).Scan(&msgCount)
	_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM tasks WHERE assignee_id = ?", memberID).Scan(&taskCount)
	_ = wdb.DB.QueryRow("SELECT COALESCE(MAX(created_at), '') FROM messages WHERE sender_id = ?", memberID).Scan(&lastActive)

	// Online status
	isOnline := false
	if h := s.hubs.Get(slug); h != nil {
		for _, m := range h.OnlineMembers() {
			if m["user_id"] == memberID {
				isOnline = true
				break
			}
		}
	}

	resp := memberDetailResp{
		ID:           memberID,
		DisplayName:  displayName,
		Role:         role,
		Title:        title,
		Bio:          bio,
		Goals:        goals,
		Color:        color,
		JoinedAt:     joinedAt,
		Permissions:  permMap,
		MessageCount: msgCount,
		TaskCount:    taskCount,
		LastActive:   lastActive,
		IsOnline:     isOnline,
	}

	// If agent, include agent-specific fields
	if role == "agent" {
		var avatar, desc, backstory, model, triggerType, toolsJSON string
		var isActive, knowledgeAccess, memoryAccess, canDelegate bool
		var createdAt string
		err = wdb.DB.QueryRow(
			`SELECT COALESCE(avatar,''), COALESCE(description,''), COALESCE(backstory,''),
			 COALESCE(model,''), COALESCE(trigger_type,'mention'), is_active,
			 knowledge_access, memory_access, can_delegate, tools, created_at
			 FROM agents WHERE id = ?`, memberID,
		).Scan(&avatar, &desc, &backstory, &model, &triggerType, &isActive,
			&knowledgeAccess, &memoryAccess, &canDelegate, &toolsJSON, &createdAt)
		if err == nil {
			var tools []any
			json.Unmarshal([]byte(toolsJSON), &tools)
			resp.Agent = &agentProfileResp{
				Avatar:          avatar,
				Description:     desc,
				Backstory:       backstory,
				Model:           model,
				TriggerType:     triggerType,
				IsActive:        isActive,
				KnowledgeAccess: knowledgeAccess,
				MemoryAccess:    memoryAccess,
				CanDelegate:     canDelegate,
				ToolCount:       len(tools),
				CreatedAt:       createdAt,
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleKickMember removes a member from the workspace. Admin only.
func (s *Server) handleKickMember(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	memberID := r.PathValue("memberID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if memberID == claims.UserID {
		writeError(w, http.StatusBadRequest, "cannot kick yourself")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Clean up overrides and guest channels first (FK constraints)
	wdb.DB.Exec("DELETE FROM permission_overrides WHERE member_id = ?", memberID)
	wdb.DB.Exec("DELETE FROM guest_channels WHERE member_id = ?", memberID)

	res, err := wdb.DB.Exec("DELETE FROM members WHERE id = ?", memberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove member")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleListRoles returns all available roles and their default permissions.
func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	type roleInfo struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}

	var result []roleInfo
	for role, perms := range roles.DefaultPermissions {
		var permList []string
		for p := range perms {
			permList = append(permList, string(p))
		}
		result = append(result, roleInfo{Name: string(role), Permissions: permList})
	}

	writeJSON(w, http.StatusOK, result)
}

// handleGetWorkspaceInfo returns workspace metadata, storage stats, and entity counts.
func (s *Server) handleGetWorkspaceInfo(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Workspace metadata from global DB
	var wsName, createdAt, createdBy string
	_ = s.global.DB.QueryRow("SELECT name, COALESCE(created_at,''), COALESCE(created_by,'') FROM workspaces WHERE slug = ?", slug).Scan(&wsName, &createdAt, &createdBy)

	// Resolve creator name
	creatorName := ""
	if createdBy != "" {
		_ = wdb.DB.QueryRow("SELECT display_name FROM members WHERE id = ?", createdBy).Scan(&creatorName)
	}

	// Entity counts
	counts := map[string]int{}
	countQueries := map[string]string{
		"members":    "SELECT COUNT(*) FROM members WHERE role NOT IN ('agent','brain')",
		"agents":     "SELECT COUNT(*) FROM agents WHERE is_system = FALSE",
		"channels":   "SELECT COUNT(*) FROM channels WHERE archived = FALSE",
		"messages":   "SELECT COUNT(*) FROM messages WHERE deleted = FALSE",
		"tasks":      "SELECT COUNT(*) FROM tasks",
		"documents":  "SELECT COUNT(*) FROM documents",
		"files":      "SELECT COUNT(*) FROM files",
		"knowledge":  "SELECT COUNT(*) FROM brain_knowledge",
		"memories":   "SELECT COUNT(*) FROM brain_memories",
		"events":     "SELECT COUNT(*) FROM calendar_events",
	}
	for key, q := range countQueries {
		var c int
		_ = wdb.DB.QueryRow(q).Scan(&c)
		counts[key] = c
	}

	// File storage size from DB
	var filesSize int64
	_ = wdb.DB.QueryRow("SELECT COALESCE(SUM(size), 0) FROM files").Scan(&filesSize)

	// Disk usage (walk workspace dir)
	wsDir := filepath.Join(s.cfg.DataDir, "workspaces", slug)
	var diskBytes int64
	_ = filepath.Walk(wsDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			diskBytes += info.Size()
		}
		return nil
	})

	// Online count
	onlineCount := 0
	if h := s.hubs.Get(slug); h != nil {
		onlineCount = len(h.OnlineMembers())
	}

	plan := "free"
	maxMembers := freePlanMemberLimit
	if s.cfg.LicenseKey != "" {
		plan = "pro"
		maxMembers = -1
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":          slug,
		"name":          wsName,
		"created_at":    createdAt,
		"created_by":    creatorName,
		"counts":        counts,
		"files_bytes":   filesSize,
		"disk_bytes":    diskBytes,
		"online_count":  onlineCount,
		"disk_display":  formatBytes(diskBytes),
		"files_display": formatBytes(filesSize),
		"plan":          plan,
		"max_members":   maxMembers,
	})
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
