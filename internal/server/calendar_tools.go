package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

func (s *Server) toolCreateCalendarEvent(slug, channelID, senderMemberID, argsJSON string) string {
	var args struct {
		Title          string `json:"title"`
		StartTime      string `json:"start_time"`
		EndTime        string `json:"end_time"`
		Description    string `json:"description"`
		Location       string `json:"location"`
		AllDay         bool   `json:"all_day"`
		RecurrenceRule string `json:"recurrence_rule"`
		AttendeeNames  []string `json:"attendee_names"`
		Reminders      []struct {
			MinutesBefore int    `json:"minutes_before"`
			Type          string `json:"type"`
		} `json:"reminders"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Title == "" || args.StartTime == "" || args.EndTime == "" {
		return "Error: title, start_time, and end_time are required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Resolve attendee names to member IDs
	var attendees []map[string]string
	for _, name := range args.AttendeeNames {
		var memberID string
		_ = wdb.DB.QueryRow(
			"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)", name,
		).Scan(&memberID)
		attendees = append(attendees, map[string]string{
			"member_id": memberID,
			"name":      name,
			"status":    "pending",
		})
	}
	if attendees == nil {
		attendees = []map[string]string{}
	}
	attendeesJSON, _ := json.Marshal(attendees)

	remindersJSON := []byte("[]")
	if len(args.Reminders) > 0 {
		remindersJSON, _ = json.Marshal(args.Reminders)
	}

	eventID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	allDay := 0
	if args.AllDay {
		allDay = 1
	}

	_, err = wdb.DB.Exec(
		`INSERT INTO calendar_events (id, title, description, location, start_time, end_time, all_day, recurrence_rule, calendar, created_by, attendees, reminders, channel_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'default', ?, ?, ?, ?, 'confirmed', ?, ?)`,
		eventID, args.Title, args.Description, args.Location,
		args.StartTime, args.EndTime, allDay, args.RecurrenceRule,
		creatorID(senderMemberID), string(attendeesJSON), string(remindersJSON),
		channelID, now, now,
	)
	if err != nil {
		return "Error creating event: " + err.Error()
	}

	// Broadcast
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventCreated, map[string]any{
		"id": eventID, "title": args.Title, "description": args.Description,
		"location": args.Location, "start_time": args.StartTime, "end_time": args.EndTime,
		"all_day": args.AllDay, "recurrence_rule": args.RecurrenceRule,
		"calendar": "default", "created_by": creatorID(senderMemberID),
		"attendees": json.RawMessage(attendeesJSON), "reminders": json.RawMessage(remindersJSON),
		"status": "confirmed", "created_at": now, "updated_at": now,
	}), "")

	result := fmt.Sprintf("Event created: \"%s\" on %s", args.Title, args.StartTime)
	if args.RecurrenceRule != "" {
		result += fmt.Sprintf(" (recurring: %s)", args.RecurrenceRule)
	}
	if len(args.AttendeeNames) > 0 {
		result += fmt.Sprintf(" with %s", strings.Join(args.AttendeeNames, ", "))
	}
	return result
}

func (s *Server) toolListCalendarEvents(slug, argsJSON string) string {
	var args struct {
		Start    string `json:"start"`
		End      string `json:"end"`
		Calendar string `json:"calendar"`
	}
	if argsJSON != "" {
		json.Unmarshal([]byte(argsJSON), &args)
	}

	if args.Start == "" {
		args.Start = time.Now().UTC().Format(time.RFC3339)
	}
	if args.End == "" {
		args.End = time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	query := `SELECT id, title, description, location, start_time, end_time, all_day, recurrence_rule, COALESCE(recurrence_parent_id,''), status, attendees
		FROM calendar_events WHERE end_time >= ? AND start_time <= ?`
	qargs := []any{args.Start, args.End}

	if args.Calendar != "" {
		query += " AND calendar = ?"
		qargs = append(qargs, args.Calendar)
	}
	query += " ORDER BY start_time ASC LIMIT 30"

	rows, err := wdb.DB.Query(query, qargs...)
	if err != nil {
		return "Error querying events"
	}
	defer rows.Close()

	var lines []string
	var recurring []hub.EventPayload
	count := 0

	for rows.Next() {
		var eid, title, desc, loc, startTime, endTime, rrule, parentID, status, attendeesStr string
		var allDay int
		rows.Scan(&eid, &title, &desc, &loc, &startTime, &endTime, &allDay, &rrule, &parentID, &status, &attendeesStr)

		if rrule != "" && parentID == "" {
			// Recurring parent — expand
			recurring = append(recurring, hub.EventPayload{
				ID: eid, Title: title, Description: desc, Location: loc,
				StartTime: startTime, EndTime: endTime, AllDay: allDay == 1,
				RecurrenceRule: rrule, Status: status,
				Attendees: json.RawMessage(attendeesStr),
			})
			continue
		}

		line := fmt.Sprintf("- **%s** | %s → %s", title, formatTime(startTime), formatTime(endTime))
		if loc != "" {
			line += " @ " + loc
		}
		if status != "confirmed" {
			line += " [" + status + "]"
		}
		line += fmt.Sprintf(" (id: %s)", eid)
		lines = append(lines, line)
		count++
	}

	// Expand recurring events
	if len(recurring) > 0 {
		rangeStart, _ := time.Parse(time.RFC3339, args.Start)
		rangeEnd, _ := time.Parse(time.RFC3339, args.End)
		for _, re := range recurring {
			expanded := expandRecurring(re, rangeStart, rangeEnd)
			for _, e := range expanded {
				line := fmt.Sprintf("- **%s** | %s → %s (recurring, id: %s)", e.Title, formatTime(e.StartTime), formatTime(e.EndTime), e.ID)
				if e.Location != "" {
					line += " @ " + e.Location
				}
				lines = append(lines, line)
				count++
			}
		}
	}

	if count == 0 {
		return "No events found in the specified time range."
	}
	return fmt.Sprintf("%d events found:\n%s", count, strings.Join(lines, "\n"))
}

func (s *Server) toolUpdateCalendarEvent(slug, argsJSON string) string {
	var args struct {
		EventID     string `json:"event_id"`
		Title       string `json:"title"`
		StartTime   string `json:"start_time"`
		EndTime     string `json:"end_time"`
		Description string `json:"description"`
		Location    string `json:"location"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.EventID == "" {
		return "Error: event_id is required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	var sets []string
	var qargs []any
	now := time.Now().UTC().Format(time.RFC3339)

	if args.Title != "" {
		sets = append(sets, "title = ?")
		qargs = append(qargs, args.Title)
	}
	if args.StartTime != "" {
		sets = append(sets, "start_time = ?")
		qargs = append(qargs, args.StartTime)
	}
	if args.EndTime != "" {
		sets = append(sets, "end_time = ?")
		qargs = append(qargs, args.EndTime)
	}
	if args.Description != "" {
		sets = append(sets, "description = ?")
		qargs = append(qargs, args.Description)
	}
	if args.Location != "" {
		sets = append(sets, "location = ?")
		qargs = append(qargs, args.Location)
	}
	if args.Status != "" {
		sets = append(sets, "status = ?")
		qargs = append(qargs, args.Status)
	}

	if len(sets) == 0 {
		return "Error: no fields to update"
	}

	sets = append(sets, "updated_at = ?")
	qargs = append(qargs, now)
	qargs = append(qargs, args.EventID)

	query := "UPDATE calendar_events SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := wdb.DB.Exec(query, qargs...)
	if err != nil {
		return "Error updating event: " + err.Error()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Event not found"
	}

	// Broadcast update
	e, _ := scanEvent(wdb.DB, args.EventID)
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventUpdated, e), "")

	return fmt.Sprintf("Event updated: \"%s\"", e.Title)
}

func (s *Server) toolDeleteCalendarEvent(slug, argsJSON string) string {
	var args struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.EventID == "" {
		return "Error: event_id is required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Get title before deleting
	var title string
	_ = wdb.DB.QueryRow("SELECT title FROM calendar_events WHERE id = ?", args.EventID).Scan(&title)

	// Delete exceptions too
	_, _ = wdb.DB.Exec("DELETE FROM calendar_events WHERE recurrence_parent_id = ?", args.EventID)

	res, err := wdb.DB.Exec("DELETE FROM calendar_events WHERE id = ?", args.EventID)
	if err != nil {
		return "Error deleting event: " + err.Error()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Event not found"
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventDeleted, hub.EventDeletedPayload{ID: args.EventID}), "")

	return fmt.Sprintf("Event deleted: \"%s\"", title)
}

// creatorID returns the sender's member ID, falling back to Brain if empty.
func creatorID(senderMemberID string) string {
	if senderMemberID != "" {
		return senderMemberID
	}
	return brain.BrainMemberID
}

// formatTime formats an ISO 8601 time string for display.
func formatTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Format("Mon Jan 2 3:04 PM")
}
