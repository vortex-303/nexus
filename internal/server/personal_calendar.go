package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Phase 1 calendar integration: bidirectional ICS subscription.
//
//   inbound: user pastes their personal calendar's ICS URL → we sync VEVENTs
//            to personal_busy_blocks (Brain reads via get_my_availability)
//
//   outbound: each user gets a stable signed subscription URL the user
//             pastes into Google Cal / iCal / Outlook → returns the
//             workspace's calendar_events as ICS, refetched by the client
//             on its own cadence.

// ----- inbound: personal calendar sync --------------------------------------

type personalCalendarStatus struct {
	ICSURL          string `json:"ics_url"`
	ShareDetails    bool   `json:"share_details"`
	LastSyncedAt    string `json:"last_synced_at,omitempty"`
	LastSyncError   string `json:"last_sync_error,omitempty"`
	EventCount      int    `json:"event_count"`
	Connected       bool   `json:"connected"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
}

func (s *Server) handleGetPersonalCalendar(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	st := readPersonalCalendarStatus(wdb, claims.UserID)
	st.SubscriptionURL = s.subscriptionURLFor(wdb, slug, claims.UserID)
	writeJSON(w, http.StatusOK, st)
}

type putPersonalCalendarReq struct {
	ICSURL       string `json:"ics_url"`
	ShareDetails bool   `json:"share_details"`
}

func (s *Server) handlePutPersonalCalendar(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req putPersonalCalendarReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ICSURL = strings.TrimSpace(req.ICSURL)
	if req.ICSURL == "" {
		writeError(w, http.StatusBadRequest, "ics_url required")
		return
	}
	// Accept the common "webcal://" prefix calendar apps export.
	if strings.HasPrefix(req.ICSURL, "webcal://") {
		req.ICSURL = "https://" + strings.TrimPrefix(req.ICSURL, "webcal://")
	}
	if !strings.HasPrefix(req.ICSURL, "http://") && !strings.HasPrefix(req.ICSURL, "https://") {
		writeError(w, http.StatusBadRequest, "ics_url must be http(s) or webcal")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	share := 0
	if req.ShareDetails {
		share = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(`
		INSERT INTO personal_calendars (user_id, ics_url, share_details, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
		  ics_url        = excluded.ics_url,
		  share_details  = excluded.share_details,
		  updated_at     = excluded.updated_at`,
		claims.UserID, req.ICSURL, share, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save failed")
		return
	}

	// Sync immediately so the user sees results.
	if err := s.syncPersonalCalendar(wdb, claims.UserID, req.ICSURL); err != nil {
		logger.WithCategory(logger.CatCalendar).Warn().Err(err).Str("user", claims.UserID).Msg("initial personal cal sync failed")
	}

	st := readPersonalCalendarStatus(wdb, claims.UserID)
	st.SubscriptionURL = s.subscriptionURLFor(wdb, slug, claims.UserID)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDeletePersonalCalendar(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}
	wdb.DB.Exec("DELETE FROM personal_calendars WHERE user_id = ?", claims.UserID)
	wdb.DB.Exec("DELETE FROM personal_busy_blocks WHERE user_id = ?", claims.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncPersonalCalendar manually re-fetches the user's ICS URL.
func (s *Server) handleSyncPersonalCalendar(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var icsURL string
	if err := wdb.DB.QueryRow("SELECT ics_url FROM personal_calendars WHERE user_id = ?", claims.UserID).Scan(&icsURL); err != nil {
		writeError(w, http.StatusNotFound, "no personal calendar configured")
		return
	}
	if err := s.syncPersonalCalendar(wdb, claims.UserID, icsURL); err != nil {
		writeError(w, http.StatusBadGateway, "sync failed: "+err.Error())
		return
	}
	st := readPersonalCalendarStatus(wdb, claims.UserID)
	st.SubscriptionURL = s.subscriptionURLFor(wdb, slug, claims.UserID)
	writeJSON(w, http.StatusOK, st)
}

func readPersonalCalendarStatus(wdb *db.WorkspaceDB, userID string) personalCalendarStatus {
	var st personalCalendarStatus
	var share int
	err := wdb.DB.QueryRow(`
		SELECT ics_url, share_details, last_synced_at, last_sync_error, event_count
		FROM personal_calendars WHERE user_id = ?`, userID).
		Scan(&st.ICSURL, &share, &st.LastSyncedAt, &st.LastSyncError, &st.EventCount)
	if err == nil {
		st.ShareDetails = share == 1
		st.Connected = true
	}
	return st
}

// syncPersonalCalendar fetches the URL, parses VEVENTs, replaces the user's
// busy-block cache transactionally. On failure, persists the error to the
// row so the UI can surface it without losing the URL.
func (s *Server) syncPersonalCalendar(wdb *db.WorkspaceDB, userID, icsURL string) error {
	now := time.Now().UTC()
	body, err := fetchICS(icsURL)
	if err != nil {
		wdb.DB.Exec(`UPDATE personal_calendars SET last_sync_error = ?, updated_at = ? WHERE user_id = ?`,
			err.Error(), now.Format(time.RFC3339), userID)
		return err
	}
	events := parseVEvents(body)

	tx, err := wdb.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM personal_busy_blocks WHERE user_id = ?", userID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO personal_busy_blocks
		(id, user_id, starts_at, ends_at, title, source_uid, fetched_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	count := 0
	cutoff := now.Add(-24 * time.Hour)
	horizon := now.Add(180 * 24 * time.Hour)
	for _, ev := range events {
		if ev.Start.IsZero() || ev.End.IsZero() {
			continue
		}
		// Drop events fully in the past, and anything beyond ~6 months.
		if ev.End.Before(cutoff) || ev.Start.After(horizon) {
			continue
		}
		_, err := stmt.Exec(id.New(), userID,
			ev.Start.UTC().Format(time.RFC3339),
			ev.End.UTC().Format(time.RFC3339),
			ev.Summary, ev.UID,
			now.Format(time.RFC3339))
		if err != nil {
			return err
		}
		count++
	}

	_, err = tx.Exec(`UPDATE personal_calendars
		SET last_synced_at = ?, last_sync_error = '', event_count = ?, updated_at = ?
		WHERE user_id = ?`,
		now.Format(time.RFC3339), count, now.Format(time.RFC3339), userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func fetchICS(url string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from calendar source", resp.StatusCode)
	}
	// 4 MB ceiling — typical personal calendars are well under this.
	return io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
}

// ----- minimal ICS parser ---------------------------------------------------

type vevent struct {
	UID     string
	Summary string
	Start   time.Time
	End     time.Time
}

// parseVEvents extracts VEVENT entries from an ICS document. Handles RFC 5545
// line unfolding (continuation lines start with space/tab) and the four
// DTSTART/DTEND date forms we care about. Ignores VTIMEZONE bodies, RRULE
// expansion (we capture the master event only — good enough for a busy
// overview; a future pass can expand recurrences).
func parseVEvents(body []byte) []vevent {
	lines := unfoldICSLines(body)
	var out []vevent
	var inEvent bool
	var cur vevent
	for _, line := range lines {
		switch {
		case line == "BEGIN:VEVENT":
			inEvent = true
			cur = vevent{}
		case line == "END:VEVENT":
			if inEvent {
				out = append(out, cur)
			}
			inEvent = false
		case inEvent:
			key, params, val := splitICSLine(line)
			switch key {
			case "UID":
				cur.UID = val
			case "SUMMARY":
				cur.Summary = unescapeICS(val)
			case "DTSTART":
				cur.Start = parseICSTime(val, params)
			case "DTEND":
				cur.End = parseICSTime(val, params)
			}
		}
	}
	// Fill in missing DTEND for all-day or zero-duration entries — assume 1 hour.
	for i := range out {
		if out[i].End.IsZero() && !out[i].Start.IsZero() {
			out[i].End = out[i].Start.Add(time.Hour)
		}
	}
	return out
}

func unfoldICSLines(body []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	var out []string
	for _, l := range raw {
		if l == "" {
			continue
		}
		if (l[0] == ' ' || l[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// splitICSLine splits a content line into (key, params, value).
//
//	DTSTART;TZID=America/Argentina/Buenos_Aires:20260505T140000
//	→ key="DTSTART", params="TZID=America/Argentina/Buenos_Aires", value="20260505T140000"
func splitICSLine(line string) (key, params, value string) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return line, "", ""
	}
	header := line[:colon]
	value = line[colon+1:]
	if semi := strings.IndexByte(header, ';'); semi >= 0 {
		key = header[:semi]
		params = header[semi+1:]
	} else {
		key = header
	}
	return key, params, value
}

// parseICSTime handles the three DTSTART/DTEND value forms we care about:
//
//   - 20260505T140000Z       (UTC)
//   - 20260505T140000        (floating, treat as UTC — pragmatic for a busy overview)
//   - 20260505                (date-only / VALUE=DATE — all-day, treat as 00:00 UTC start)
//
// TZID is currently ignored — we capture the local time as-if-UTC, which is
// good enough for "is this person busy" overlap. Refining to real timezones
// is a future pass once we ship Phase 1.
func parseICSTime(val, params string) time.Time {
	if strings.Contains(params, "VALUE=DATE") || (len(val) == 8 && !strings.Contains(val, "T")) {
		t, err := time.Parse("20060102", val)
		if err != nil {
			return time.Time{}
		}
		return t
	}
	for _, layout := range []string{
		"20060102T150405Z",
		"20060102T150405",
	} {
		if t, err := time.Parse(layout, val); err == nil {
			return t
		}
	}
	return time.Time{}
}

func unescapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\,", ",")
	s = strings.ReplaceAll(s, "\\;", ";")
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\N", "\n")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

// ----- outbound: subscription URL + ICS feed --------------------------------

// subscriptionURLFor returns the user's per-workspace ICS subscription URL,
// minting a token on first call. Token is opaque, 32 hex chars, never rotates
// unless the user explicitly disconnects.
func (s *Server) subscriptionURLFor(wdb *db.WorkspaceDB, slug, userID string) string {
	var token string
	err := wdb.DB.QueryRow("SELECT token FROM workspace_calendar_subscriptions WHERE user_id = ?", userID).Scan(&token)
	if err == sql.ErrNoRows {
		buf := make([]byte, 16)
		rand.Read(buf)
		token = hex.EncodeToString(buf)
		if _, err := wdb.DB.Exec("INSERT INTO workspace_calendar_subscriptions (user_id, token) VALUES (?, ?)", userID, token); err != nil {
			return ""
		}
	} else if err != nil {
		return ""
	}
	base := s.publicBaseURL()
	return fmt.Sprintf("%s/api/calendar/%s/%s.ics?token=%s", base, slug, userID, token)
}

// handleCalendarSubscription serves the workspace calendar as ICS to the
// pasted subscription URL. Public — auth via the per-user token.
func (s *Server) handleCalendarSubscription(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	userID := r.PathValue("userID")
	token := r.URL.Query().Get("token")
	if slug == "" || userID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		http.Error(w, "workspace error", http.StatusNotFound)
		return
	}
	var stored string
	if err := wdb.DB.QueryRow("SELECT token FROM workspace_calendar_subscriptions WHERE user_id = ?", userID).Scan(&stored); err != nil || stored != token {
		http.Error(w, "invalid subscription", http.StatusForbidden)
		return
	}

	// Fetch all events the user is involved in: created or invited.
	rows, err := wdb.DB.Query(`
		SELECT id, title, description, location, start_time, end_time, recurrence_rule, status, created_at
		FROM calendar_events
		WHERE created_by = ? OR attendees LIKE '%'||?||'%'
		ORDER BY start_time ASC`, userID, userID)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Nexus//Workspace Calendar//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")
	fmt.Fprintf(&b, "X-WR-CALNAME:Nexus — %s\r\n", slug)
	for rows.Next() {
		var eid, title, desc, loc, st, et, rrule, status, created string
		if err := rows.Scan(&eid, &title, &desc, &loc, &st, &et, &rrule, &status, &created); err != nil {
			continue
		}
		b.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&b, "UID:%s@nexus\r\n", eid)
		fmt.Fprintf(&b, "DTSTAMP:%s\r\n", formatICSTime(created))
		fmt.Fprintf(&b, "DTSTART:%s\r\n", formatICSTime(st))
		fmt.Fprintf(&b, "DTEND:%s\r\n", formatICSTime(et))
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", escapeICS(title))
		if desc != "" {
			fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", escapeICS(desc))
		}
		if loc != "" {
			fmt.Fprintf(&b, "LOCATION:%s\r\n", escapeICS(loc))
		}
		if rrule != "" {
			fmt.Fprintf(&b, "RRULE:%s\r\n", rrule)
		}
		switch status {
		case "tentative":
			b.WriteString("STATUS:TENTATIVE\r\n")
		case "cancelled":
			b.WriteString("STATUS:CANCELLED\r\n")
		default:
			b.WriteString("STATUS:CONFIRMED\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Disposition", "inline; filename=\"nexus.ics\"")
	w.Write([]byte(b.String()))
}

// handleEventICSDownload returns one event as an .ics attachment for the
// "Add to Calendar" button. Auth required.
func (s *Server) handleEventICSDownload(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	eventID := r.PathValue("eventID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}
	var ev struct {
		ID, Title, Desc, Loc, Start, End, RRule, Status, Created string
	}
	err = wdb.DB.QueryRow(`SELECT id, title, description, location, start_time, end_time, recurrence_rule, status, created_at
		FROM calendar_events WHERE id = ?`, eventID).
		Scan(&ev.ID, &ev.Title, &ev.Desc, &ev.Loc, &ev.Start, &ev.End, &ev.RRule, &ev.Status, &ev.Created)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Nexus//Calendar//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\nBEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s@nexus\r\n", ev.ID)
	fmt.Fprintf(&b, "DTSTAMP:%s\r\n", formatICSTime(ev.Created))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", formatICSTime(ev.Start))
	fmt.Fprintf(&b, "DTEND:%s\r\n", formatICSTime(ev.End))
	fmt.Fprintf(&b, "SUMMARY:%s\r\n", escapeICS(ev.Title))
	if ev.Desc != "" {
		fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", escapeICS(ev.Desc))
	}
	if ev.Loc != "" {
		fmt.Fprintf(&b, "LOCATION:%s\r\n", escapeICS(ev.Loc))
	}
	if ev.RRule != "" {
		fmt.Fprintf(&b, "RRULE:%s\r\n", ev.RRule)
	}
	b.WriteString("END:VEVENT\r\nEND:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.ics\"", ev.ID))
	w.Write([]byte(b.String()))
}

// publicBaseURL is the absolute origin a calendar app should fetch from.
// The subscription URL is meant to be pasted into Google Cal / iCal / Outlook,
// so it must be absolute. Order:
//
//  1. NEXUS_PUBLIC_URL env (explicit override — best for reverse-proxy setups)
//  2. https://<cfg.Domain> if Domain is set (production autocert path)
//  3. http://localhost<cfg.Listen> in dev (Listen is :PORT)
func (s *Server) publicBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("NEXUS_PUBLIC_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if s.cfg.Domain != "" {
		return "https://" + s.cfg.Domain
	}
	listen := s.cfg.Listen
	if strings.HasPrefix(listen, ":") {
		return "http://localhost" + listen
	}
	return "http://" + listen
}

// ----- availability: busy/free overlap for member set ----------------------

type availabilityBlock struct {
	UserID  string `json:"user_id"`
	Title   string `json:"title,omitempty"`
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
	Source  string `json:"source"`
}

func (s *Server) handleAvailability(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to required")
		return
	}
	memberCSV := r.URL.Query().Get("member_ids")
	if memberCSV == "" {
		memberCSV = claims.UserID
	}
	memberIDs := strings.Split(memberCSV, ",")

	out := []availabilityBlock{}
	for _, uid := range memberIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		// Personal busy blocks (only revealed for the requesting user; for
		// others, we expose busy/free without titles unless they opted in).
		share := 0
		wdb.DB.QueryRow("SELECT share_details FROM personal_calendars WHERE user_id = ?", uid).Scan(&share)
		showTitle := uid == claims.UserID || share == 1

		rows, err := wdb.DB.Query(`SELECT title, starts_at, ends_at FROM personal_busy_blocks
			WHERE user_id = ? AND ends_at >= ? AND starts_at <= ? ORDER BY starts_at ASC`, uid, from, to)
		if err == nil {
			for rows.Next() {
				var title, st, et string
				if err := rows.Scan(&title, &st, &et); err != nil {
					continue
				}
				blk := availabilityBlock{UserID: uid, StartAt: st, EndAt: et, Source: "personal"}
				if showTitle {
					blk.Title = title
				}
				out = append(out, blk)
			}
			rows.Close()
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"blocks": out})
}

// GetUserAvailability is the brain-tool surface: returns busy blocks for the
// requesting member only. Caller is responsible for windowing.
func (s *Server) GetUserAvailability(wdb *db.WorkspaceDB, userID, from, to string) ([]availabilityBlock, error) {
	rows, err := wdb.DB.Query(`SELECT title, starts_at, ends_at FROM personal_busy_blocks
		WHERE user_id = ? AND ends_at >= ? AND starts_at <= ? ORDER BY starts_at ASC`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []availabilityBlock
	for rows.Next() {
		var title, st, et string
		if err := rows.Scan(&title, &st, &et); err != nil {
			continue
		}
		out = append(out, availabilityBlock{UserID: userID, StartAt: st, EndAt: et, Title: title, Source: "personal"})
	}
	return out, nil
}

