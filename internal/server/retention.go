package server

import (
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Retention windows for log-style tables. 60 days lines up with the user-
// chosen audit horizon (Slack Free is 90; we picked tighter to keep
// per-workspace SQLite small for self-hosting). The cron runs once a day.
const retentionDays = 60

// scheduleRetention registers a daily prune of log-style tables across
// every workspace. Tables we trim:
//
//   - activity_stream     — pulse feed (sidebar Activity)
//   - brain_action_log    — Brain reply log (Settings → Console → Changes)
//   - brain_settings_log  — config-change history
//   - brain_traces        — v3 turn traces (cascade-deletes brain_trace_steps)
//
// Tables NOT trimmed: messages, calendar_events, tasks, documents,
// brain_memories, brain_managed_sessions — those are durable artifacts,
// not logs. Settings storage (brain_settings) is also untouched.
func (s *Server) scheduleRetention() {
	s.cron.AddFunc("@daily", func() { s.pruneOldLogs() })
	// Run once at boot so a freshly-deployed instance isn't sitting on
	// months of pre-retention rows. Cheap — DELETE with a date predicate
	// hits an indexed created_at column on every table here.
	go s.pruneOldLogs()
	logger.WithCategory(logger.CatSystem).Info().Int("days", retentionDays).Msg("retention scheduler started")
}

func (s *Server) pruneOldLogs() {
	slugs, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err != nil {
		return
	}
	defer slugs.Close()
	var workspaceSlugs []string
	for slugs.Next() {
		var slug string
		if slugs.Scan(&slug) == nil {
			workspaceSlugs = append(workspaceSlugs, slug)
		}
	}
	for _, slug := range workspaceSlugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}
		s.pruneWorkspaceLogs(wdb, slug)
	}
}

func (s *Server) pruneWorkspaceLogs(wdb *db.WorkspaceDB, slug string) {
	cutoff := "datetime('now', '-' || ? || ' days')"
	tables := []struct {
		name        string
		dateColumn  string
		extraWhere  string
	}{
		{"activity_stream", "created_at", ""},
		{"brain_action_log", "created_at", ""},
		{"brain_settings_log", "changed_at", ""},
		{"brain_traces", "created_at", ""},
		// brain_trace_steps is FK'd to brain_traces by trace_id (no
		// cascade defined), so trim it on the same window directly.
		{"brain_trace_steps", "created_at", ""},
	}
	for _, t := range tables {
		// Some workspaces predate one or another of these tables; the
		// migration system creates them idempotently, but a missing
		// table mid-migration would error here. Best-effort: log and
		// move on so a single missing table doesn't block the rest.
		query := "DELETE FROM " + t.name + " WHERE " + t.dateColumn + " < " + cutoff
		if t.extraWhere != "" {
			query += " AND " + t.extraWhere
		}
		res, err := wdb.DB.Exec(query, retentionDays)
		if err != nil {
			logger.WithCategory(logger.CatSystem).Warn().
				Str("workspace", slug).Str("table", t.name).Err(err).
				Msg("retention prune failed (table missing? best-effort)")
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			logger.WithCategory(logger.CatSystem).Info().
				Str("workspace", slug).Str("table", t.name).Int64("pruned", n).
				Msg("retention pruned old rows")
		}
	}
}
