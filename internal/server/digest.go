package server

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/logger"
)

// registerDigestCron hooks the daily superadmin digest into the server cron.
// Fires hourly and self-gates by hour-of-day + last-sent-date so we send at
// most one digest per UTC day. Hourly cadence makes it robust to short
// server downtime around the target hour.
func (s *Server) registerDigestCron() {
	if os.Getenv("DIGEST_ENABLED") == "false" {
		logger.WithCategory(logger.CatSystem).Info().Msg("daily digest disabled via DIGEST_ENABLED=false")
		return
	}
	s.cron.AddFunc("@hourly", func() { s.maybeSendDailyDigest() })
}

// maybeSendDailyDigest sends the digest if we're in the target hour window
// and haven't already sent today.
func (s *Server) maybeSendDailyDigest() {
	targetHour := 9
	if v := os.Getenv("DIGEST_HOUR"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h <= 23 {
			targetHour = h
		}
	}
	now := time.Now().UTC()
	if now.Hour() != targetHour {
		return
	}
	today := now.Format("2006-01-02")

	var lastSent string
	s.global.DB.QueryRow("SELECT value FROM digest_state WHERE key = ?", "waitlist_digest_last_sent").Scan(&lastSent)
	if lastSent == today {
		return
	}

	if err := s.SendDailyDigest(); err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("daily digest send failed")
		return
	}
	_, _ = s.global.DB.Exec(
		"INSERT INTO digest_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"waitlist_digest_last_sent", today,
	)
}

// SendDailyDigest queries the last 24h of waitlist signups and emails the
// superadmin a summary. Returns nil and skips silently when there are zero
// new signups (no "all quiet" emails). Exported so the CLI can trigger it.
func (s *Server) SendDailyDigest() error {
	if s.cfg.ResendAPIKey == "" {
		return fmt.Errorf("resend not configured (no RESEND_API_KEY)")
	}

	recipient := os.Getenv("DIGEST_RECIPIENT")
	if recipient == "" {
		recipient = os.Getenv("SUPERADMIN_EMAIL")
	}
	if recipient == "" {
		recipient = "nruggieri@gmail.com"
	}

	rows, err := s.global.DB.Query(
		`SELECT email, plan, created_at FROM waitlist
		 WHERE created_at >= datetime('now', '-24 hours')
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return fmt.Errorf("query waitlist: %w", err)
	}
	defer rows.Close()

	type entry struct{ email, plan, createdAt string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.email, &e.plan, &e.createdAt); err == nil {
			entries = append(entries, e)
		}
	}

	if len(entries) == 0 {
		logger.WithCategory(logger.CatSystem).Debug().Msg("daily digest: 0 new signups, skipping")
		return nil
	}

	var totalWaitlist int
	s.global.DB.QueryRow("SELECT COUNT(*) FROM waitlist").Scan(&totalWaitlist)

	subject := fmt.Sprintf("Nexus daily digest — %d new signup%s",
		len(entries),
		map[bool]string{true: "", false: "s"}[len(entries) == 1],
	)

	var rowsHTML strings.Builder
	for _, e := range entries {
		when := e.createdAt
		if t, err := time.Parse(time.RFC3339, e.createdAt); err == nil {
			when = t.Format("Jan 2 15:04 UTC")
		}
		rowsHTML.WriteString(fmt.Sprintf(
			`<tr>
				<td style="padding:8px 12px;border-bottom:1px solid #28282f;font-family:monospace;font-size:13px;">%s</td>
				<td style="padding:8px 12px;border-bottom:1px solid #28282f;font-size:13px;color:#f97316;">%s</td>
				<td style="padding:8px 12px;border-bottom:1px solid #28282f;font-size:12px;color:#a0a0a8;">%s</td>
			</tr>`,
			html.EscapeString(e.email),
			html.EscapeString(e.plan),
			html.EscapeString(when),
		))
	}

	body := fmt.Sprintf(`<div style="font-family:-apple-system,BlinkMacSystemFont,sans-serif;max-width:560px;margin:0 auto;padding:32px 20px;background:#fff;color:#111;">
		<h2 style="color:#f97316;margin:0 0 8px 0;">Nexus daily digest</h2>
		<p style="margin:0 0 24px 0;color:#606068;font-size:14px;">%s · %d new in last 24h · %d total on waitlist</p>
		<table style="width:100%%;border-collapse:collapse;background:#0e0e12;color:#f0f0f0;border-radius:8px;overflow:hidden;">
			<thead>
				<tr style="background:#141418;">
					<th style="text-align:left;padding:10px 12px;font-size:11px;text-transform:uppercase;letter-spacing:1px;color:#a0a0a8;border-bottom:1px solid #28282f;">Email</th>
					<th style="text-align:left;padding:10px 12px;font-size:11px;text-transform:uppercase;letter-spacing:1px;color:#a0a0a8;border-bottom:1px solid #28282f;">Plan</th>
					<th style="text-align:left;padding:10px 12px;font-size:11px;text-transform:uppercase;letter-spacing:1px;color:#a0a0a8;border-bottom:1px solid #28282f;">Joined</th>
				</tr>
			</thead>
			<tbody>%s</tbody>
		</table>
		<p style="margin:24px 0 0 0;font-size:14px;">
			<a href="https://nexusteams.dev/admin" style="color:#f97316;text-decoration:none;font-weight:600;">Open admin →</a>
		</p>
		<p style="margin:32px 0 0 0;font-size:11px;color:#606068;">
			Nexus · Daily digest fires at %02d:00 UTC. Set DIGEST_ENABLED=false to disable, DIGEST_HOUR=N to change the hour.
		</p>
	</div>`,
		time.Now().UTC().Format("Mon Jan 2, 2006"),
		len(entries),
		totalWaitlist,
		rowsHTML.String(),
		digestTargetHour(),
	)

	if err := s.sendEmail(recipient, subject, body); err != nil {
		return err
	}
	logger.WithCategory(logger.CatSystem).Info().
		Str("recipient", recipient).
		Int("signups", len(entries)).
		Msg("daily digest sent")
	return nil
}

func digestTargetHour() int {
	if v := os.Getenv("DIGEST_HOUR"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h <= 23 {
			return h
		}
	}
	return 9
}

// handleAdminSendDigest fires the digest immediately. Useful for testing
// without waiting until the next scheduled hour.
func (s *Server) handleAdminSendDigest(w http.ResponseWriter, r *http.Request) {
	if err := s.SendDailyDigest(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
