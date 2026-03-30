package server

import (
	"encoding/json"
	"time"

	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

type eventReminder struct {
	MinutesBefore int    `json:"minutes_before"`
	Type          string `json:"type"` // "notification" or "email"
}

// scheduleCalendarReminders registers the calendar reminder cron job.
func (s *Server) scheduleCalendarReminders() {
	s.cron.AddFunc("@every 1m", func() { s.checkCalendarReminders() })
	logger.WithCategory(logger.CatCalendar).Info().Msg("reminder scheduler started")
}

func (s *Server) checkCalendarReminders() {
	// Check scheduled agent tasks
	s.checkAgentTasks()
	// Also check agent triggers
	s.checkCalendarAgentTriggers()

	slugs := s.hubs.ActiveSlugs()
	now := time.Now().UTC()

	for _, slug := range slugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		// Find events starting within the next 60 minutes that have reminders
		windowEnd := now.Add(60 * time.Minute).Format(time.RFC3339)
		nowStr := now.Format(time.RFC3339)

		rows, err := wdb.DB.Query(
			`SELECT id, title, start_time, reminders FROM calendar_events
			 WHERE status = 'confirmed' AND reminders != '[]' AND start_time <= ? AND start_time >= ?`,
			windowEnd, nowStr,
		)
		if err != nil {
			continue
		}

		for rows.Next() {
			var eventID, title, startTime, remindersStr string
			if err := rows.Scan(&eventID, &title, &startTime, &remindersStr); err != nil {
				continue
			}

			var reminders []eventReminder
			if err := json.Unmarshal([]byte(remindersStr), &reminders); err != nil {
				continue
			}

			eventStart, err := time.Parse(time.RFC3339, startTime)
			if err != nil {
				continue
			}

			for _, rem := range reminders {
				triggerTime := eventStart.Add(-time.Duration(rem.MinutesBefore) * time.Minute)
				if now.Before(triggerTime) || now.After(triggerTime.Add(1*time.Minute)) {
					continue
				}

				// Dedup check
				reminderKey := eventID + ":" + startTime + ":" + string(rune(rem.MinutesBefore))
				var exists int
				_ = wdb.DB.QueryRow(
					"SELECT 1 FROM calendar_reminders_sent WHERE reminder_key = ?", reminderKey,
				).Scan(&exists)
				if exists == 1 {
					continue
				}

				// Mark as sent
				_, _ = wdb.DB.Exec(
					"INSERT INTO calendar_reminders_sent (id, event_id, reminder_key, sent_at) VALUES (?, ?, ?, ?)",
					id.New(), eventID, reminderKey, nowStr,
				)

				if rem.Type == "notification" || rem.Type == "" {
					// WebSocket notification
					h := s.hubs.Get(slug)
					h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventReminder, hub.EventReminderPayload{
						EventID:   eventID,
						Title:     title,
						StartTime: startTime,
						Minutes:   rem.MinutesBefore,
					}), "")
					logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("event", title).Int("minutes_before", rem.MinutesBefore).Msg("reminder sent")
				} else if rem.Type == "email" {
					// Send email reminders to attendees
					s.sendEventReminderEmails(slug, eventID, title, startTime, rem.MinutesBefore)
				}
			}
		}
		rows.Close()
	}
}

func (s *Server) sendEventReminderEmails(slug, eventID, title, startTime string, minutesBefore int) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	var attendeesStr string
	_ = wdb.DB.QueryRow("SELECT attendees FROM calendar_events WHERE id = ?", eventID).Scan(&attendeesStr)

	var attendees []struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	json.Unmarshal([]byte(attendeesStr), &attendees)

	for _, a := range attendees {
		if a.Email == "" {
			continue
		}
		subject := "Reminder: " + title
		body := "This is a reminder that \"" + title + "\" starts at " + startTime + "."
		s.sendOutboundEmail(slug, a.Email, subject, body, "")
	}
}
