package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nexus-chat/nexus/internal/auth"
)

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

	query := `SELECT id, pulse_type, actor_id, actor_name, channel_id, entity_id, summary, detail, source, created_at
		FROM activity_stream`
	var conditions []string
	var args []any

	if typeFilter := r.URL.Query().Get("type"); typeFilter != "" {
		if strings.HasSuffix(typeFilter, ".*") {
			prefix := strings.TrimSuffix(typeFilter, "*")
			conditions = append(conditions, "pulse_type LIKE ?")
			args = append(args, prefix+"%")
		} else {
			conditions = append(conditions, "pulse_type = ?")
			args = append(args, typeFilter)
		}
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
