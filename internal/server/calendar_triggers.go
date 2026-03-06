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
	slugs := s.hubs.ActiveSlugs()
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
						s.handleAgentMention(slug, targetChannel, "", "Calendar", content, agent)
					}
				}
			}
		}
		eventRows.Close()
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
