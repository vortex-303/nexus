package server

import (
	"net/http"

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
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Role        string            `json:"role"`
	Permissions map[string]bool   `json:"permissions"`
}

// handleGetMember returns a member's details including effective permissions.
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

	var displayName, role string
	err = wdb.DB.QueryRow(
		"SELECT display_name, role FROM members WHERE id = ?", memberID,
	).Scan(&displayName, &role)
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	checker := roles.NewChecker(wdb.DB)
	perms := checker.Resolve(memberID)
	permMap := make(map[string]bool, len(perms))
	for k, v := range perms {
		permMap[string(k)] = v
	}

	writeJSON(w, http.StatusOK, memberDetailResp{
		ID:          memberID,
		DisplayName: displayName,
		Role:        role,
		Permissions: permMap,
	})
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
