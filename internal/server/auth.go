package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
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
	emailVerified := true
	if s.accountRequired() {
		emailVerified = false
	}
	_, err = s.global.DB.Exec(
		"INSERT INTO accounts (id, email, password_hash, display_name, email_verified) VALUES (?, ?, ?, ?, ?)",
		accountID, req.Email, string(hash), req.DisplayName, emailVerified,
	)
	if err != nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}

	// Send verification email when account is required
	if s.accountRequired() {
		code := fmt.Sprintf("%06d", randomInt(1000000))
		expiresAt := time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)
		_, _ = s.global.DB.Exec(
			"INSERT INTO email_verifications (id, email, code, expires_at) VALUES (?, ?, ?, ?)",
			id.New(), req.Email, code, expiresAt,
		)
		go func() {
			if err := s.sendVerificationEmail(req.Email, code); err != nil {
				logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("verification email failed")
			}
		}()
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
	var isSuperadmin, isBanned, emailVerified bool
	err := s.global.DB.QueryRow(
		"SELECT id, password_hash, display_name, is_superadmin, banned, email_verified FROM accounts WHERE email = ?",
		req.Email,
	).Scan(&accountID, &hash, &displayName, &isSuperadmin, &isBanned, &emailVerified)
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

	if s.accountRequired() && !emailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "verify_email",
			"message": "Please verify your email address before logging in",
			"email": req.Email,
		})
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

	// Send confirmation email
	if s.accountRequired() {
		var email string
		if s.global.DB.QueryRow("SELECT email FROM accounts WHERE id = ?", claims.AccountID).Scan(&email) == nil {
			go s.sendPasswordChangedEmail(email)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleForgotPassword initiates a password reset flow.
// POST /api/auth/forgot
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, "email required")
		return
	}

	// Always return 200 to not leak account existence
	defer writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	var accountID string
	err := s.global.DB.QueryRow("SELECT id FROM accounts WHERE email = ?", req.Email).Scan(&accountID)
	if err != nil {
		return // account doesn't exist, but don't reveal that
	}

	// Generate secure token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)

	_, err = s.global.DB.Exec(
		"INSERT INTO password_resets (id, email, token, expires_at) VALUES (?, ?, ?, ?)",
		id.New(), req.Email, token, expiresAt,
	)
	if err != nil {
		return
	}

	go func() {
		if err := s.sendPasswordResetEmail(req.Email, token); err != nil {
			logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("password reset email failed")
		}
	}()
}

// handleResetPassword completes a password reset using a token.
// POST /api/auth/reset
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "token and new_password required")
		return
	}

	var resetID, email, expiresAt string
	var used bool
	err := s.global.DB.QueryRow(
		"SELECT id, email, expires_at, used FROM password_resets WHERE token = ?", req.Token,
	).Scan(&resetID, &email, &expiresAt, &used)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired reset link")
		return
	}

	if used {
		writeError(w, http.StatusBadRequest, "this reset link has already been used")
		return
	}

	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || time.Now().UTC().After(expires) {
		writeError(w, http.StatusBadRequest, "this reset link has expired")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	_, err = s.global.DB.Exec("UPDATE accounts SET password_hash = ? WHERE email = ?", string(hash), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	// Mark token as used
	s.global.DB.Exec("UPDATE password_resets SET used = TRUE WHERE id = ?", resetID)

	// Send confirmation
	go s.sendPasswordChangedEmail(email)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVerifyEmail verifies an email address with a 6-digit code.
// POST /api/auth/verify
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "email and code required")
		return
	}

	var verID, expiresAt string
	var verified bool
	err := s.global.DB.QueryRow(
		"SELECT id, expires_at, verified FROM email_verifications WHERE email = ? AND code = ? ORDER BY created_at DESC LIMIT 1",
		req.Email, req.Code,
	).Scan(&verID, &expiresAt, &verified)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid verification code")
		return
	}

	if verified {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_verified"})
		return
	}

	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || time.Now().UTC().After(expires) {
		writeError(w, http.StatusBadRequest, "verification code has expired")
		return
	}

	// Mark verification as verified
	s.global.DB.Exec("UPDATE email_verifications SET verified = TRUE WHERE id = ?", verID)
	// Mark account as verified
	s.global.DB.Exec("UPDATE accounts SET email_verified = TRUE WHERE email = ?", req.Email)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// randomInt returns a random integer in [0, max) using crypto/rand.
func randomInt(max int) int {
	b := make([]byte, 4)
	rand.Read(b)
	return int(uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3])) % max
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
