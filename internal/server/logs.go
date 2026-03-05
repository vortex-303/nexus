package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/logger"
)

// handleGetLogs returns structured log entries from the ring buffer.
// Admin sees all; non-admin is rejected.
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	ring := logger.Ring()
	if ring == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "categories": logger.AllCategories})
		return
	}

	opts := logger.QueryOpts{}

	if cat := r.URL.Query().Get("category"); cat != "" {
		opts.Category = cat
	}
	if lvl := r.URL.Query().Get("level"); lvl != "" {
		opts.Level = lvl
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = t
		} else {
			// Try parsing as duration like "1h", "24h", "7d"
			if d, err := parseDuration(since); err == nil {
				opts.Since = time.Now().Add(-d)
			}
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			opts.Until = t
		}
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	if offset := r.URL.Query().Get("offset"); offset != "" {
		if n, err := strconv.Atoi(offset); err == nil && n >= 0 {
			opts.Offset = n
		}
	}

	if opts.Limit == 0 {
		opts.Limit = 200
	}

	entries := ring.Query(opts)
	if entries == nil {
		entries = []logger.LogEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries":    entries,
		"total":      ring.Count(),
		"categories": logger.AllCategories,
	})
}

// parseDuration parses "1h", "24h", "7d" etc.
func parseDuration(s string) (time.Duration, error) {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
