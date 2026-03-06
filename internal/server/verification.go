package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nexus-chat/nexus/internal/logger"
)

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
		"from":    "Nexus <onboarding@resend.dev>",
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
