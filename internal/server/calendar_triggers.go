package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/logger"
)

// calendarTriggerConfig defines when a calendar-triggered agent should fire.
type calendarTriggerConfig struct {
	EventTypes    []string `json:"event_types"`    // e.g. "meeting_start", "deadline_approaching"
	MinutesBefore int      `json:"minutes_before"` // how many minutes before event to trigger
}

// checkCalendarAgentTriggers runs as part of the reminder cron to fire matching agents.
func (s *Server) checkCalendarAgentTriggers() {
	// Also check events with agent_id set directly
	s.checkDirectAgentEvents()

	slugs := s.hubs.ActiveSlugs() // ActiveSlugs OK here — trigger-based agents need a connected client to deliver results
	now := time.Now().UTC()

	for _, slug := range slugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		// Find agents with trigger_type = "calendar_event"
		rows, err := wdb.DB.Query(
			`SELECT id, name, trigger_config FROM agents WHERE trigger_type = 'calendar_event' AND is_active = 1`,
		)
		if err != nil {
			continue
		}

		type agentTrigger struct {
			agentID string
			agentName string
			config  calendarTriggerConfig
		}
		var triggers []agentTrigger

		for rows.Next() {
			var aid, aname, configStr string
			if err := rows.Scan(&aid, &aname, &configStr); err != nil {
				continue
			}
			var cfg calendarTriggerConfig
			if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
				continue
			}
			triggers = append(triggers, agentTrigger{agentID: aid, agentName: aname, config: cfg})
		}
		rows.Close()

		if len(triggers) == 0 {
			continue
		}

		// Check upcoming events
		windowEnd := now.Add(60 * time.Minute).Format(time.RFC3339)
		nowStr := now.Format(time.RFC3339)

		eventRows, err := wdb.DB.Query(
			`SELECT id, title, start_time, COALESCE(channel_id,'') FROM calendar_events
			 WHERE status = 'confirmed' AND start_time <= ? AND start_time >= ?`,
			windowEnd, nowStr,
		)
		if err != nil {
			continue
		}

		for eventRows.Next() {
			var eventID, title, startTime, channelID string
			if err := eventRows.Scan(&eventID, &title, &startTime, &channelID); err != nil {
				continue
			}

			eventStart, err := time.Parse(time.RFC3339, startTime)
			if err != nil {
				continue
			}
			minutesUntil := int(eventStart.Sub(now).Minutes())

			for _, trigger := range triggers {
				if minutesUntil > trigger.config.MinutesBefore {
					continue
				}

				// Dedup: check if we already triggered this agent for this event
				reminderKey := fmt.Sprintf("agent:%s:%s", trigger.agentID, eventID)
				var exists int
				_ = wdb.DB.QueryRow(
					"SELECT 1 FROM calendar_reminders_sent WHERE reminder_key = ?", reminderKey,
				).Scan(&exists)
				if exists == 1 {
					continue
				}

				// Mark as triggered
				_, _ = wdb.DB.Exec(
					"INSERT INTO calendar_reminders_sent (id, event_id, reminder_key, sent_at) VALUES (?, ?, ?, ?)",
					fmt.Sprintf("at_%s_%s", trigger.agentID, eventID), eventID, reminderKey, nowStr,
				)

				// Fire agent
				targetChannel := channelID
				if targetChannel == "" {
					// Use first available channel as fallback
					_ = wdb.DB.QueryRow("SELECT id FROM channels LIMIT 1").Scan(&targetChannel)
				}

				if targetChannel != "" {
					content := fmt.Sprintf("Calendar event starting: \"%s\" at %s", title, startTime)
					agent := s.loadAgentByID(slug, trigger.agentID)
					if agent != nil {
						logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("agent", trigger.agentName).Str("event", title).Msg("triggering agent for event")
						s.handleAgentMention(slug, targetChannel, "", "Calendar", content, agent, time.Now())
					}
				}
			}
		}
		eventRows.Close()
	}
}

// checkDirectAgentEvents fires Brain/agents directly assigned to calendar events via agent_id.
// Queries ALL workspaces (not just active ones) so scheduled events fire even when nobody is online.
func (s *Server) checkDirectAgentEvents() {
	// Get all workspace slugs from global DB
	slugRows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err != nil {
		return
	}
	var slugs []string
	for slugRows.Next() {
		var slug string
		slugRows.Scan(&slug)
		slugs = append(slugs, slug)
	}
	slugRows.Close()

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	// Look at events starting within the last 1 minute (the cron runs every 1m)
	windowStart := now.Add(-1 * time.Minute).Format(time.RFC3339)

	logger.WithCategory(logger.CatCalendar).Debug().Int("workspaces", len(slugs)).Str("now", nowStr).Str("window_start", windowStart).Msg("checking direct agent events")

	for _, slug := range slugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		// First check how many agent events exist at all for debugging
		var totalAgentEvents int
		_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM calendar_events WHERE agent_id != ''").Scan(&totalAgentEvents)
		if totalAgentEvents > 0 {
			logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Int("agent_events", totalAgentEvents).Str("window", windowStart+" to "+nowStr).Msg("agent events in workspace")
		}

		rows, err := wdb.DB.Query(
			`SELECT id, title, description, start_time, agent_id, model, COALESCE(channel_id,'')
			 FROM calendar_events
			 WHERE status = 'confirmed' AND agent_id != '' AND start_time <= ? AND start_time > ?`,
			nowStr, windowStart,
		)
		if err != nil {
			continue
		}

		for rows.Next() {
			var eventID, title, description, startTime, agentID, model, channelID string
			if err := rows.Scan(&eventID, &title, &description, &startTime, &agentID, &model, &channelID); err != nil {
				continue
			}

			// Dedup
			reminderKey := fmt.Sprintf("agent_direct:%s:%s", agentID, eventID)
			var exists int
			_ = wdb.DB.QueryRow("SELECT 1 FROM calendar_reminders_sent WHERE reminder_key = ?", reminderKey).Scan(&exists)
			if exists == 1 {
				continue
			}
			_, _ = wdb.DB.Exec(
				"INSERT INTO calendar_reminders_sent (id, event_id, reminder_key, sent_at) VALUES (?, ?, ?, ?)",
				fmt.Sprintf("ad_%s_%s", agentID, eventID), eventID, reminderKey, nowStr,
			)

			// Resolve channel
			targetChannel := channelID
			if targetChannel == "" {
				_ = wdb.DB.QueryRow("SELECT id FROM channels LIMIT 1").Scan(&targetChannel)
			}
			if targetChannel == "" {
				continue
			}

			prompt := fmt.Sprintf("Scheduled task from calendar event \"%s\"", title)
			if description != "" {
				prompt += "\n\n" + description
			}

			// Brain vs custom agent
			if agentID == "brain" {
				logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("event", title).Str("model", model).Msg("triggering Brain for scheduled event")
				s.handleBrainMentionWithTools(slug, targetChannel, "", "Scheduler", prompt, time.Now(), model)
			} else {
				agent := s.loadAgentByID(slug, agentID)
				if agent != nil {
					logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("agent", agent.Name).Str("event", title).Msg("triggering agent for scheduled event")
					s.handleAgentMention(slug, targetChannel, "", "Scheduler", prompt, agent, time.Now())
				}
			}

			// Mark event as executed (dispatch confirmed — dedup prevents re-fire)
			_, _ = wdb.DB.Exec(
				"UPDATE calendar_events SET executed_at = ?, updated_at = ? WHERE id = ?",
				nowStr, nowStr, eventID,
			)
		}
		rows.Close()
	}
}

// postEventToChannel posts event details to the linked channel at event start.
func (s *Server) postEventToChannel(slug string, event hub.EventPayload) {
	if event.ChannelID == "" {
		return
	}

	content := fmt.Sprintf("**%s** is starting now", event.Title)
	if event.Location != "" {
		content += fmt.Sprintf("\nLocation: %s", event.Location)
	}
	if event.Description != "" {
		content += fmt.Sprintf("\n%s", event.Description)
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ChannelID:  event.ChannelID,
		SenderName: "Calendar",
		Content:    content,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}), "")
}
