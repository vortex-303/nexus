package server

import (
	"net/http"
	"strings"

	"github.com/nexus-chat/nexus/internal/auth"
)

// handleWaitlist accepts an email + plan and stores it in the waitlist table.
func (s *Server) handleWaitlist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Plan  string `json:"plan"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "valid email required")
		return
	}
	plan := req.Plan
	if plan == "" {
		plan = "pro"
	}

	_, err := s.global.DB.Exec(
		"INSERT INTO waitlist (email, plan) VALUES (?, ?) ON CONFLICT(email) DO UPDATE SET plan = excluded.plan",
		email, plan,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to join waitlist")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListWaitlist returns all waitlist entries. Superadmin only.
func (s *Server) handleListWaitlist(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || !claims.SuperAdmin {
		writeError(w, http.StatusForbidden, "superadmin only")
		return
	}

	rows, err := s.global.DB.Query("SELECT id, email, plan, created_at FROM waitlist ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var entries []map[string]any
	for rows.Next() {
		var id int
		var email, plan, createdAt string
		if rows.Scan(&id, &email, &plan, &createdAt) == nil {
			entries = append(entries, map[string]any{
				"id": id, "email": email, "plan": plan, "created_at": createdAt,
			})
		}
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, entries)
}
