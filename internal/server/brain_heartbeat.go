package server

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// scheduleHeartbeats registers heartbeat checks with the cron scheduler.
func (s *Server) scheduleHeartbeats() {
	s.cron.AddFunc("@every 1m", func() { s.checkHeartbeats() })
	logger.WithCategory(logger.CatBrain).Info().Msg("heartbeat scheduler started")
}

// checkHeartbeats iterates all workspaces and runs due heartbeat schedules.
func (s *Server) checkHeartbeats() {
	slugs := s.hubs.ActiveSlugs()
	now := time.Now()

	for _, slug := range slugs {
		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" {
			continue
		}

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		schedules := brain.ParseHeartbeat(brainDir)

		for _, sched := range schedules {
			if !sched.ShouldRun(now) {
				continue
			}

			go s.runHeartbeat(slug, sched, apiKey, model)
		}
	}
}

// runHeartbeat executes a single heartbeat schedule.
func (s *Server) runHeartbeat(slug string, sched brain.HeartbeatSchedule, apiKey, model string) {
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("schedule", sched.Name).Msg("running heartbeat")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("heartbeat workspace error")
		return
	}

	// Find the target channel
	channelID := resolveChannelByName(wdb.DB, sched.Channel)
	if channelID == "" {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Str("channel", sched.Channel).Msg("heartbeat channel not found")
		return
	}

	// Build context for the heartbeat
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	systemPrompt, err := brain.BuildSystemPrompt(brainDir)
	if err != nil {
		return
	}

	// Add memory context
	memoryContext := brain.BuildMemoryContext(wdb.DB, "")
	if memoryContext != "" {
		systemPrompt += "\n\n---\n\n" + memoryContext
	}

	// Build task context for the heartbeat
	taskContext := buildTaskContext(wdb.DB)

	// Construct the heartbeat prompt
	userMessage := fmt.Sprintf(
		"You are running a scheduled heartbeat action: **%s**\n\n"+
			"Instructions: %s\n\n"+
			"Current date/time: %s\n\n"+
			"%s\n\n"+
			"Generate your response for the #%s channel. Be concise and useful.",
		sched.Name, sched.Action, time.Now().Format("Monday, January 2 2006 3:04 PM"),
		taskContext, sched.Channel,
	)

	messages := []brain.Message{
		{Role: "user", Content: userMessage},
	}

	resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
	client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)
	response, usage, err := client.Complete(systemPrompt, messages)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("heartbeat LLM error")
		return
	}
	s.trackUsage(slug, usage, resolvedModel, "heartbeat", channelID, "")

	response = strings.TrimSpace(response)
	if response == "" {
		return
	}

	s.sendBrainMessage(slug, channelID, "", response)

	// Log the action
	brain.LogAction(wdb.DB, id.New(), brain.ActionHeartbeat, channelID,
		sched.Name, truncate(response, 500), model, nil)

	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("schedule", sched.Name).Msg("heartbeat completed")
}

// resolveChannelByName finds a channel ID by its name.
func resolveChannelByName(db *sql.DB, name string) string {
	var channelID string
	_ = db.QueryRow(
		"SELECT id FROM channels WHERE LOWER(name) = LOWER(?) AND archived = FALSE",
		name,
	).Scan(&channelID)
	return channelID
}

// buildTaskContext gathers task info for heartbeat prompts.
func buildTaskContext(db *sql.DB) string {
	rows, err := db.Query(`
		SELECT t.title, t.status, t.priority, COALESCE(m.display_name, ''), t.due_date
		FROM tasks t LEFT JOIN members m ON m.id = t.assignee_id
		WHERE t.status NOT IN ('done', 'cancelled')
		ORDER BY
			CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
			t.created_at DESC
		LIMIT 30
	`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var title, status, priority, assignee, dueDate string
		rows.Scan(&title, &status, &priority, &assignee, &dueDate)
		line := fmt.Sprintf("- [%s] %s (%s)", status, title, priority)
		if assignee != "" {
			line += " → " + assignee
		}
		if dueDate != "" {
			line += " due:" + dueDate
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return "No open tasks."
	}
	return fmt.Sprintf("## Open Tasks (%d)\n%s", len(lines), strings.Join(lines, "\n"))
}
