package server

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type registerResp struct {
	AccountID string `json:"account_id"`
}

// handleRegister creates an optional account (email/password).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Email
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	accountID := id.New()
	_, err = s.global.DB.Exec(
		"INSERT INTO accounts (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)",
		accountID, req.Email, string(hash), req.DisplayName,
	)
	if err != nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}

	writeJSON(w, http.StatusCreated, registerResp{AccountID: accountID})
}

type loginReq struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	WorkspaceSlug string `json:"workspace_slug"`
}

type loginResp struct {
	Token         string `json:"token"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
}

// handleLogin authenticates with email/password and issues a JWT.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	var accountID, hash, displayName string
	var isSuperadmin, isBanned bool
	err := s.global.DB.QueryRow(
		"SELECT id, password_hash, display_name, is_superadmin, banned FROM accounts WHERE email = ?",
		req.Email,
	).Scan(&accountID, &hash, &displayName, &isSuperadmin, &isBanned)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if isBanned {
		writeError(w, http.StatusForbidden, "account has been suspended")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// If workspace specified, find their member record
	role := ""
	userID := accountID
	wsSlug := req.WorkspaceSlug

	if wsSlug != "" {
		wdb, err := s.ws.Open(wsSlug)
		if err != nil {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		err = wdb.DB.QueryRow(
			"SELECT id, role FROM members WHERE account_id = ?", accountID,
		).Scan(&userID, &role)
		if err != nil {
			writeError(w, http.StatusForbidden, "not a member of this workspace")
			return
		}
	} else if !isSuperadmin {
		// Auto-scope: if user belongs to exactly 1 workspace, scope the token directly
		wsSlug, userID, role = s.findSingleWorkspace(accountID)
	}

	var opts []func(*auth.Claims)
	if isSuperadmin {
		opts = append(opts, auth.WithSuperAdmin())
	}

	token, err := s.jwt.Issue(userID, displayName, wsSlug, role, accountID, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, loginResp{Token: token, WorkspaceSlug: wsSlug})
}

// handleSwitchWorkspace exchanges a valid token for a workspace-scoped token.
func (s *Server) handleSwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.AccountID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		WorkspaceSlug string `json:"workspace_slug"`
	}
	if err := readJSON(r, &req); err != nil || req.WorkspaceSlug == "" {
		writeError(w, http.StatusBadRequest, "workspace_slug required")
		return
	}

	wdb, err := s.ws.Open(req.WorkspaceSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	var userID, role string
	err = wdb.DB.QueryRow(
		"SELECT id, role FROM members WHERE account_id = ?", claims.AccountID,
	).Scan(&userID, &role)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// Look up display name
	var displayName string
	s.global.DB.QueryRow("SELECT display_name FROM accounts WHERE id = ?", claims.AccountID).Scan(&displayName)

	var opts []func(*auth.Claims)
	if claims.SuperAdmin {
		opts = append(opts, auth.WithSuperAdmin())
	}

	token, err := s.jwt.Issue(userID, displayName, req.WorkspaceSlug, role, claims.AccountID, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, loginResp{Token: token})
}

// findSingleWorkspace checks if an account belongs to exactly 1 workspace.
// Returns (slug, memberID, role) if so, or ("", accountID, "") if not.
func (s *Server) findSingleWorkspace(accountID string) (string, string, string) {
	rows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err != nil {
		return "", accountID, ""
	}
	defer rows.Close()

	var matchSlug, matchUserID, matchRole string
	matches := 0
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) != nil {
			continue
		}
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}
		var uid, role string
		if wdb.DB.QueryRow("SELECT id, role FROM members WHERE account_id = ?", accountID).Scan(&uid, &role) == nil {
			matches++
			matchSlug = slug
			matchUserID = uid
			matchRole = role
			if matches > 1 {
				return "", accountID, ""
			}
		}
	}
	if matches == 1 {
		return matchSlug, matchUserID, matchRole
	}
	return "", accountID, ""
}

// handleGetMe returns the authenticated user's profile.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.AccountID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var email, displayName, createdAt string
	err := s.global.DB.QueryRow(
		"SELECT email, display_name, created_at FROM accounts WHERE id = ?",
		claims.AccountID,
	).Scan(&email, &displayName, &createdAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"id":           claims.AccountID,
		"email":        email,
		"display_name": displayName,
		"created_at":   createdAt,
	})
}

// handleUpdateMe updates the authenticated user's profile.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.AccountID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DisplayName != "" {
		_, err := s.global.DB.Exec("UPDATE accounts SET display_name = ? WHERE id = ?", req.DisplayName, claims.AccountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update profile")
			return
		}
		// Update display_name in all workspace member rows linked to this account
		slugs, _ := s.global.DB.Query("SELECT slug FROM workspaces")
		if slugs != nil {
			defer slugs.Close()
			for slugs.Next() {
				var slug string
				if slugs.Scan(&slug) == nil {
					if wdb, err := s.ws.Open(slug); err == nil {
						_, _ = wdb.DB.Exec("UPDATE members SET display_name = ? WHERE account_id = ?", req.DisplayName, claims.AccountID)
					}
				}
			}
		}
	}

	if req.Email != "" {
		if !strings.Contains(req.Email, "@") {
			writeError(w, http.StatusBadRequest, "invalid email address")
			return
		}
		if req.Password == "" {
			writeError(w, http.StatusBadRequest, "password required to change email")
			return
		}
		var hash string
		var isSuperadmin bool
		if err := s.global.DB.QueryRow("SELECT password_hash, is_superadmin FROM accounts WHERE id = ?", claims.AccountID).Scan(&hash, &isSuperadmin); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify account")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
			writeError(w, http.StatusUnauthorized, "incorrect password")
			return
		}
		if isSuperadmin {
			writeError(w, http.StatusForbidden, "superadmin email cannot be changed from UI")
			return
		}
		_, err := s.global.DB.Exec("UPDATE accounts SET email = ? WHERE id = ?", req.Email, claims.AccountID)
		if err != nil {
			writeError(w, http.StatusConflict, "email already in use")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleChangePassword changes the authenticated user's password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.AccountID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password and new_password required")
		return
	}

	var hash string
	err := s.global.DB.QueryRow("SELECT password_hash FROM accounts WHERE id = ?", claims.AccountID).Scan(&hash)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	_, err = s.global.DB.Exec("UPDATE accounts SET password_hash = ? WHERE id = ?", string(newHash), claims.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type workspaceEntry struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// handleListWorkspaces returns workspaces the authenticated account belongs to.
func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.AccountID == "" {
		writeError(w, http.StatusForbidden, "no account linked")
		return
	}

	// Get all workspace slugs
	rows, err := s.global.DB.Query("SELECT slug, name FROM workspaces")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}
	defer rows.Close()

	var result []workspaceEntry
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			continue
		}
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}
		var role, displayName string
		err = wdb.DB.QueryRow(
			"SELECT role, display_name FROM members WHERE account_id = ?", claims.AccountID,
		).Scan(&role, &displayName)
		if err != nil {
			continue // not a member
		}
		dn := displayName
		if name != "" {
			dn = name
		}
		result = append(result, workspaceEntry{Slug: slug, DisplayName: dn, Role: role})
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspaces": result})
}
