package server

import (
	"net/http"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

// OrgNode represents a node in the org chart (human or agent).
type OrgNode struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // "human", "agent", "system_agent"
	Name         string `json:"name"`
	Role         string `json:"role"`
	Title        string `json:"title,omitempty"`
	Bio          string `json:"bio,omitempty"`
	Goals        string `json:"goals,omitempty"`
	ReportsTo    string `json:"reports_to"`
	IsActive     bool   `json:"is_active"`
	Avatar       string `json:"avatar,omitempty"`
	Online       bool   `json:"online,omitempty"`
	MessageCount int    `json:"message_count"`
	TaskCount    int    `json:"task_count"`
	LastActive   string `json:"last_active,omitempty"`
}

func (s *Server) handleGetOrgChart(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	// Ensure Brain system agent is seeded
	s.ensureBrainMember(slug)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var nodes []OrgNode

	// Load Brain as root node
	var brainNode OrgNode
	err = wdb.DB.QueryRow(`SELECT id, display_name FROM members WHERE role = 'brain'`).Scan(&brainNode.ID, &brainNode.Name)
	if err == nil {
		brainNode.Type = "system_agent"
		brainNode.Role = "System Brain"
		brainNode.IsActive = true
		brainNode.Avatar = "🧠"
		nodes = append(nodes, brainNode)
	}

	// Load human members
	rows, err := wdb.DB.Query(`
		SELECT id, display_name, role, COALESCE(title,''), COALESCE(bio,''), COALESCE(goals,''), COALESCE(reports_to,'')
		FROM members WHERE role != 'agent' AND role != 'brain'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n OrgNode
			if err := rows.Scan(&n.ID, &n.Name, &n.Role, &n.Title, &n.Bio, &n.Goals, &n.ReportsTo); err == nil {
				n.Type = "human"
				n.IsActive = true
				nodes = append(nodes, n)
			}
		}
	}

	// Load agents (skip system agents — Brain is already added as root from members)
	agentRows, err := wdb.DB.Query(`
		SELECT id, name, role, is_active, avatar
		FROM agents WHERE is_system = FALSE`)
	if err == nil {
		defer agentRows.Close()
		for agentRows.Next() {
			var n OrgNode
			if err := agentRows.Scan(&n.ID, &n.Name, &n.Role, &n.IsActive, &n.Avatar); err == nil {
				n.Type = "agent"
				n.ReportsTo = "brain" // agents report to Brain by default
				nodes = append(nodes, n)
			}
		}
	}

	// Load org roles (vacant or filled positions)
	roleRows, err := wdb.DB.Query(`
		SELECT id, title, description, reports_to, COALESCE(filled_by,''), filled_type, created_at
		FROM org_roles`)
	if err == nil {
		defer roleRows.Close()
		for roleRows.Next() {
			var role OrgRole
			if err := roleRows.Scan(&role.ID, &role.Title, &role.Description, &role.ReportsTo, &role.FilledBy, &role.FilledType, &role.CreatedAt); err == nil {
				if role.FilledBy == "" {
					// Vacant slot — add as a role_slot node
					nodes = append(nodes, OrgNode{
						ID:        role.ID,
						Type:      "role_slot",
						Name:      role.Title,
						Role:      role.Description,
						ReportsTo: role.ReportsTo,
						IsActive:  true,
					})
				}
			}
		}
	}

	// Attach per-node stats from messages and tasks tables
	msgCounts := map[string]int{}
	lastActive := map[string]string{}
	msgRows, err := wdb.DB.Query(`SELECT sender_id, COUNT(*), MAX(created_at) FROM messages GROUP BY sender_id`)
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var sid string
			var cnt int
			var la string
			if msgRows.Scan(&sid, &cnt, &la) == nil {
				msgCounts[sid] = cnt
				lastActive[sid] = la
			}
		}
	}

	taskCounts := map[string]int{}
	taskRows, err := wdb.DB.Query(`SELECT assignee_id, COUNT(*) FROM tasks WHERE assignee_id != '' GROUP BY assignee_id`)
	if err == nil {
		defer taskRows.Close()
		for taskRows.Next() {
			var aid string
			var cnt int
			if taskRows.Scan(&aid, &cnt) == nil {
				taskCounts[aid] = cnt
			}
		}
	}

	for i := range nodes {
		nodes[i].MessageCount = msgCounts[nodes[i].ID]
		nodes[i].TaskCount = taskCounts[nodes[i].ID]
		if la, ok := lastActive[nodes[i].ID]; ok {
			nodes[i].LastActive = la
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (s *Server) handleUpdateOrgPosition(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		NodeID    string `json:"node_id"`
		ReportsTo string `json:"reports_to"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Update reports_to on members table
	_, err = wdb.DB.Exec("UPDATE members SET reports_to = ? WHERE id = ?", req.ReportsTo, req.NodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUpdateMemberProfile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	memberID := r.PathValue("memberID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Allow admin to edit anyone, members to edit own profile
	if claims.Role != "admin" && claims.UserID != memberID {
		writeError(w, http.StatusForbidden, "can only edit your own profile")
		return
	}

	var req struct {
		Title *string `json:"title"`
		Bio   *string `json:"bio"`
		Goals *string `json:"goals"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	if req.Title != nil {
		_, _ = wdb.DB.Exec("UPDATE members SET title = ? WHERE id = ?", *req.Title, memberID)
	}
	if req.Bio != nil {
		_, _ = wdb.DB.Exec("UPDATE members SET bio = ? WHERE id = ?", *req.Bio, memberID)
	}
	if req.Goals != nil {
		_, _ = wdb.DB.Exec("UPDATE members SET goals = ? WHERE id = ?", *req.Goals, memberID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// OrgRole represents a defined position in the org chart.
type OrgRole struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ReportsTo   string `json:"reports_to"`
	FilledBy    string `json:"filled_by,omitempty"`
	FilledType  string `json:"filled_type,omitempty"` // "human", "agent", or ""
	CreatedAt   string `json:"created_at,omitempty"`
}

func (s *Server) handleCreateOrgRole(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		ReportsTo   string `json:"reports_to"`
	}
	if err := readJSON(r, &req); err != nil || req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	roleID := "role_" + id.New()
	_, err = wdb.DB.Exec(
		"INSERT INTO org_roles (id, title, description, reports_to) VALUES (?, ?, ?, ?)",
		roleID, req.Title, req.Description, req.ReportsTo,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create role")
		return
	}

	writeJSON(w, http.StatusCreated, OrgRole{
		ID: roleID, Title: req.Title, Description: req.Description, ReportsTo: req.ReportsTo,
	})
}

func (s *Server) handleListOrgRoles(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(`SELECT id, title, description, reports_to, COALESCE(filled_by,''), filled_type, created_at FROM org_roles ORDER BY created_at ASC`)
	if err != nil {
		writeJSON(w, http.StatusOK, []OrgRole{})
		return
	}
	defer rows.Close()

	var roles []OrgRole
	for rows.Next() {
		var r OrgRole
		if err := rows.Scan(&r.ID, &r.Title, &r.Description, &r.ReportsTo, &r.FilledBy, &r.FilledType, &r.CreatedAt); err == nil {
			roles = append(roles, r)
		}
	}
	if roles == nil {
		roles = []OrgRole{}
	}
	writeJSON(w, http.StatusOK, roles)
}

func (s *Server) handleUpdateOrgRole(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	roleID := r.PathValue("roleID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		ReportsTo   *string `json:"reports_to"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	if req.Title != nil {
		_, _ = wdb.DB.Exec("UPDATE org_roles SET title = ? WHERE id = ?", *req.Title, roleID)
	}
	if req.Description != nil {
		_, _ = wdb.DB.Exec("UPDATE org_roles SET description = ? WHERE id = ?", *req.Description, roleID)
	}
	if req.ReportsTo != nil {
		_, _ = wdb.DB.Exec("UPDATE org_roles SET reports_to = ? WHERE id = ?", *req.ReportsTo, roleID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteOrgRole(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	roleID := r.PathValue("roleID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	_, err = wdb.DB.Exec("DELETE FROM org_roles WHERE id = ?", roleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFillOrgRole(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	roleID := r.PathValue("roleID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		FilledBy   string `json:"filled_by"`
		FilledType string `json:"filled_type"` // "human", "agent", or "" to vacate
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	if req.FilledBy == "" {
		// Vacate role
		_, err = wdb.DB.Exec("UPDATE org_roles SET filled_by = NULL, filled_type = '' WHERE id = ?", roleID)
	} else {
		_, err = wdb.DB.Exec("UPDATE org_roles SET filled_by = ?, filled_type = ? WHERE id = ?",
			req.FilledBy, req.FilledType, roleID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
