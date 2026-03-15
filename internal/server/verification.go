package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nexus-chat/nexus/internal/logger"
)

// frontendBaseURL returns the base URL for email links.
func (s *Server) frontendBaseURL() string {
	if s.cfg.Domain != "" {
		return "https://" + s.cfg.Domain
	}
	return "https://nexus-workspace.fly.dev"
}

// accountRequired returns true when running on cloud (Resend configured).
// When true, all users must register with email + password.
func (s *Server) accountRequired() bool {
	return s.cfg.ResendAPIKey != ""
}

// handleAuthConfig returns public auth configuration so the frontend
// knows whether to show email+password fields.
// GET /api/auth/config
func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"require_account": s.accountRequired(),
	})
}

// sendEmail sends a transactional email via Resend API.
func (s *Server) sendEmail(to, subject, htmlBody string) error {
	payload := map[string]any{
		"from":    "Nexus <noreply@nexusteams.dev>",
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.ResendAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("resend returned status %d: %v", resp.StatusCode, errBody)
	}
	return nil
}

// sendInviteEmail sends an invite email with code + link via Resend.
func (s *Server) sendInviteEmail(to, inviterName, wsName, inviteCodeVal, inviteURL string) error {
	subject := fmt.Sprintf("You're invited to join %s", wsName)
	html := fmt.Sprintf(
		`<div style="font-family:sans-serif;max-width:480px;margin:0 auto;padding:40px 20px;">
		<h2 style="color:#f97316;margin-bottom:8px;">Nexus</h2>
		<p>%s has invited you to join <strong>%s</strong>.</p>
		<p>Your invite code:</p>
		<div style="font-size:28px;font-weight:bold;letter-spacing:6px;padding:16px;background:#1a1a1a;border-radius:8px;text-align:center;color:#fff;margin:16px 0;">%s</div>
		<p style="font-size:14px;">Or click the link below to join directly:</p>
		<p><a href="%s" style="color:#f97316;">%s</a></p>
		<p style="color:#888;font-size:13px;">This invite expires in 24 hours.</p>
		</div>`, inviterName, wsName, inviteCodeVal, inviteURL, inviteURL)

	if err := s.sendEmail(to, subject, html); err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("email", to).Msg("invite email failed")
		return err
	}
	return nil
}

// sendPasswordResetEmail sends a password reset link.
func (s *Server) sendPasswordResetEmail(to, token string) error {
	resetURL := s.frontendBaseURL() + "/login?reset=" + token
	subject := "Reset your password"
	html := fmt.Sprintf(
		`<div style="font-family:sans-serif;max-width:480px;margin:0 auto;padding:40px 20px;">
		<h2 style="color:#f97316;margin-bottom:8px;">Nexus</h2>
		<p>We received a request to reset your password.</p>
		<p>Click the link below to set a new password:</p>
		<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#f97316;color:#fff;border-radius:8px;text-decoration:none;font-weight:bold;">Reset Password</a></p>
		<p style="color:#888;font-size:13px;">This link expires in 1 hour. If you didn't request this, you can ignore this email.</p>
		</div>`, resetURL)

	if err := s.sendEmail(to, subject, html); err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("email", to).Msg("password reset email failed")
		return err
	}
	return nil
}

// sendPasswordChangedEmail sends a confirmation that a password was changed.
func (s *Server) sendPasswordChangedEmail(to string) error {
	subject := "Your password was changed"
	html := `<div style="font-family:sans-serif;max-width:480px;margin:0 auto;padding:40px 20px;">
		<h2 style="color:#f97316;margin-bottom:8px;">Nexus</h2>
		<p>Your password was successfully changed.</p>
		<p style="color:#888;font-size:13px;">If you didn't make this change, please reset your password immediately or contact support.</p>
		</div>`

	if err := s.sendEmail(to, subject, html); err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("email", to).Msg("password changed email failed")
		return err
	}
	return nil
}

// sendVerificationEmail sends a 6-digit verification code.
func (s *Server) sendVerificationEmail(to, code string) error {
	subject := "Verify your email"
	html := fmt.Sprintf(
		`<div style="font-family:sans-serif;max-width:480px;margin:0 auto;padding:40px 20px;">
		<h2 style="color:#f97316;margin-bottom:8px;">Nexus</h2>
		<p>Enter this code to verify your email address:</p>
		<div style="font-size:28px;font-weight:bold;letter-spacing:6px;padding:16px;background:#1a1a1a;border-radius:8px;text-align:center;color:#fff;margin:16px 0;">%s</div>
		<p style="color:#888;font-size:13px;">This code expires in 15 minutes.</p>
		</div>`, code)

	if err := s.sendEmail(to, subject, html); err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("email", to).Msg("verification email failed")
		return err
	}
	return nil
}
