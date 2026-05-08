package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nexus-chat/nexus/internal/auth"
)

// narrativeActivityTypes is the allowlist of pulse_types surfaced to all
// workspace members in the sidebar Activity feed. The goal is "what
// happened in the workspace this week" — so we exclude per-message
// chatter (`message.sent`, `agent.responded`) and admin-internal events.
//
// Admins can override by passing ?audience=admin to see the firehose
// (used by Brain Settings → Console → Diagnostics).
var narrativeActivityTypes = map[string]bool{
	"task.created":          true,
	"task.updated":          true,
	"task.completed":        true,
	"event.created":         true,
	"event.updated":         true,
	"document.created":      true,
	"document.updated":      true,
	"file.uploaded":         true,
	"channel.created":       true,
	"integration.received":  true,
	"integration.connected": true,
	"member.joined":         true,
	"member.left":           true,
	"role.changed":          true,
	"decision.captured":     true,
}

func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
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

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	// Resolve audience scope. ?audience=admin gates on real admin role —
	// silently downgrades non-admins to the user feed to avoid leaking
	// raw chatter to invited members.
	audience := r.URL.Query().Get("audience")
	if audience == "admin" && claims.Role != "admin" {
		audience = "user"
	}

	query := `SELECT id, pulse_type, actor_id, actor_name, channel_id, entity_id, summary, detail, source, created_at
		FROM activity_stream`
	var conditions []string
	var args []any

	// Narrative allowlist for the default user-facing feed. Admin audience
	// bypasses this — they get every pulse. Explicit ?type=… still wins
	// over the default allowlist so callers can target a single feed.
	typeFilter := r.URL.Query().Get("type")
	if typeFilter != "" {
		if strings.HasSuffix(typeFilter, ".*") {
			prefix := strings.TrimSuffix(typeFilter, "*")
			conditions = append(conditions, "pulse_type LIKE ?")
			args = append(args, prefix+"%")
		} else {
			conditions = append(conditions, "pulse_type = ?")
			args = append(args, typeFilter)
		}
	} else if audience != "admin" {
		allowed := make([]string, 0, len(narrativeActivityTypes))
		for t := range narrativeActivityTypes {
			allowed = append(allowed, "?")
			args = append(args, t)
		}
		conditions = append(conditions, "pulse_type IN ("+strings.Join(allowed, ",")+")")
	}

	if before := r.URL.Query().Get("before"); before != "" {
		conditions = append(conditions, "created_at < ?")
		args = append(args, before)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	activities := []activityEntry{}
	for rows.Next() {
		var a activityEntry
		if err := rows.Scan(&a.ID, &a.PulseType, &a.ActorID, &a.ActorName,
			&a.ChannelID, &a.EntityID, &a.Summary, &a.Detail, &a.Source, &a.CreatedAt); err != nil {
			continue
		}
		activities = append(activities, a)
	}

	writeJSON(w, http.StatusOK, map[string]any{"activities": activities})
}

func (s *Server) handleActivityStats(w http.ResponseWriter, r *http.Request) {
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

	days := 365
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 730 {
			days = n
		}
	}

	// Daily counts
	rows, err := wdb.DB.Query(
		`SELECT DATE(created_at) as day, COUNT(*) as cnt
		 FROM activity_stream
		 WHERE created_at >= DATE('now', '-' || ? || ' days')
		 GROUP BY day ORDER BY day`,
		days,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type dayCount struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}
	var dailyCounts []dayCount
	for rows.Next() {
		var dc dayCount
		if err := rows.Scan(&dc.Date, &dc.Count); err != nil {
			continue
		}
		dailyCounts = append(dailyCounts, dc)
	}

	// Type counts
	typeRows, err := wdb.DB.Query(
		`SELECT pulse_type, COUNT(*) FROM activity_stream
		 WHERE created_at >= DATE('now', '-' || ? || ' days')
		 GROUP BY pulse_type`,
		days,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer typeRows.Close()

	typeCounts := map[string]int{}
	total := 0
	for typeRows.Next() {
		var t string
		var c int
		if err := typeRows.Scan(&t, &c); err != nil {
			continue
		}
		typeCounts[t] = c
		total += c
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"daily_counts": dailyCounts,
		"type_counts":  typeCounts,
		"total":        total,
	})
}
