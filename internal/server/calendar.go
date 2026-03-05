package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

type createEventReq struct {
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Location       string          `json:"location"`
	StartTime      string          `json:"start_time"`
	EndTime        string          `json:"end_time"`
	AllDay         bool            `json:"all_day"`
	RecurrenceRule string          `json:"recurrence_rule"`
	Color          string          `json:"color"`
	Calendar       string          `json:"calendar"`
	Attendees      json.RawMessage `json:"attendees"`
	Reminders      json.RawMessage `json:"reminders"`
	ChannelID      string          `json:"channel_id"`
}

type updateEventReq struct {
	Title          *string          `json:"title"`
	Description    *string          `json:"description"`
	Location       *string          `json:"location"`
	StartTime      *string          `json:"start_time"`
	EndTime        *string          `json:"end_time"`
	AllDay         *bool            `json:"all_day"`
	RecurrenceRule *string          `json:"recurrence_rule"`
	Color          *string          `json:"color"`
	Calendar       *string          `json:"calendar"`
	Attendees      json.RawMessage  `json:"attendees"`
	Reminders      json.RawMessage  `json:"reminders"`
	ChannelID      *string          `json:"channel_id"`
	Status         *string          `json:"status"`
}

var validEventStatuses = map[string]bool{
	"confirmed": true, "tentative": true, "cancelled": true,
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req createEventReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title required")
		return
	}
	if req.StartTime == "" || req.EndTime == "" {
		writeError(w, http.StatusBadRequest, "start_time and end_time required")
		return
	}
	if req.Calendar == "" {
		req.Calendar = "default"
	}
	if req.Attendees == nil {
		req.Attendees = json.RawMessage("[]")
	}
	if req.Reminders == nil {
		req.Reminders = json.RawMessage("[]")
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	eventID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	allDay := 0
	if req.AllDay {
		allDay = 1
	}

	var channelID sql.NullString
	if req.ChannelID != "" {
		channelID = sql.NullString{String: req.ChannelID, Valid: true}
	}

	_, err = wdb.DB.Exec(
		`INSERT INTO calendar_events (id, title, description, location, start_time, end_time, all_day, recurrence_rule, color, calendar, created_by, attendees, reminders, channel_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'confirmed', ?, ?)`,
		eventID, req.Title, req.Description, req.Location,
		req.StartTime, req.EndTime, allDay, req.RecurrenceRule,
		req.Color, req.Calendar, claims.UserID,
		string(req.Attendees), string(req.Reminders),
		channelID, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create event")
		return
	}

	event := hub.EventPayload{
		ID:             eventID,
		Title:          req.Title,
		Description:    req.Description,
		Location:       req.Location,
		StartTime:      req.StartTime,
		EndTime:        req.EndTime,
		AllDay:         req.AllDay,
		RecurrenceRule: req.RecurrenceRule,
		Color:          req.Color,
		Calendar:       req.Calendar,
		CreatedBy:      claims.UserID,
		Attendees:      req.Attendees,
		Reminders:      req.Reminders,
		ChannelID:      req.ChannelID,
		Status:         "confirmed",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventCreated, event), "")

	writeJSON(w, http.StatusCreated, event)
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
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

	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	cal := r.URL.Query().Get("calendar")

	query := `SELECT ce.id, ce.title, ce.description, ce.location, ce.start_time, ce.end_time, ce.all_day, ce.recurrence_rule, COALESCE(ce.recurrence_parent_id,''), ce.color, COALESCE(m.color,''), ce.calendar, ce.created_by, ce.attendees, ce.reminders, COALESCE(ce.channel_id,''), ce.status, ce.created_at, ce.updated_at
		FROM calendar_events ce LEFT JOIN members m ON m.id = ce.created_by WHERE 1=1`
	var args []any

	if start != "" {
		query += " AND ce.end_time >= ?"
		args = append(args, start)
	}
	if end != "" {
		query += " AND ce.start_time <= ?"
		args = append(args, end)
	}
	if cal != "" {
		query += " AND ce.calendar = ?"
		args = append(args, cal)
	}
	query += " ORDER BY ce.start_time ASC"

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	events := []hub.EventPayload{}
	var recurring []hub.EventPayload

	for rows.Next() {
		var e hub.EventPayload
		var attendeesStr, remindersStr, memberColor string
		var allDay int
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &e.Location,
			&e.StartTime, &e.EndTime, &allDay, &e.RecurrenceRule,
			&e.RecurrenceParentID, &e.Color, &memberColor, &e.Calendar, &e.CreatedBy,
			&attendeesStr, &remindersStr, &e.ChannelID, &e.Status,
			&e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		e.AllDay = allDay == 1
		e.Attendees = json.RawMessage(attendeesStr)
		e.Reminders = json.RawMessage(remindersStr)
		// Use event color if set, otherwise use creator's member color
		if e.Color == "" && memberColor != "" {
			e.DisplayColor = memberColor
		}

		if e.RecurrenceRule != "" && e.RecurrenceParentID == "" {
			recurring = append(recurring, e)
		} else {
			events = append(events, e)
		}
	}

	// Expand recurring events within the requested range
	if len(recurring) > 0 && start != "" && end != "" {
		rangeStart, err1 := time.Parse(time.RFC3339, start)
		rangeEnd, err2 := time.Parse(time.RFC3339, end)
		if err1 == nil && err2 == nil {
			for _, re := range recurring {
				expanded := expandRecurring(re, rangeStart, rangeEnd)
				events = append(events, expanded...)
			}
		}
	} else {
		// No range filter: include recurring parent as-is
		events = append(events, recurring...)
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
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

	e, err := scanEvent(wdb.DB, eventID)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	eventID := r.PathValue("eventID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req updateEventReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var sets []string
	var args []any
	now := time.Now().UTC().Format(time.RFC3339)

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *req.Description)
	}
	if req.Location != nil {
		sets = append(sets, "location = ?")
		args = append(args, *req.Location)
	}
	if req.StartTime != nil {
		sets = append(sets, "start_time = ?")
		args = append(args, *req.StartTime)
	}
	if req.EndTime != nil {
		sets = append(sets, "end_time = ?")
		args = append(args, *req.EndTime)
	}
	if req.AllDay != nil {
		allDay := 0
		if *req.AllDay {
			allDay = 1
		}
		sets = append(sets, "all_day = ?")
		args = append(args, allDay)
	}
	if req.RecurrenceRule != nil {
		sets = append(sets, "recurrence_rule = ?")
		args = append(args, *req.RecurrenceRule)
	}
	if req.Color != nil {
		sets = append(sets, "color = ?")
		args = append(args, *req.Color)
	}
	if req.Calendar != nil {
		sets = append(sets, "calendar = ?")
		args = append(args, *req.Calendar)
	}
	if req.Attendees != nil {
		sets = append(sets, "attendees = ?")
		args = append(args, string(req.Attendees))
	}
	if req.Reminders != nil {
		sets = append(sets, "reminders = ?")
		args = append(args, string(req.Reminders))
	}
	if req.ChannelID != nil {
		if *req.ChannelID == "" {
			sets = append(sets, "channel_id = NULL")
		} else {
			sets = append(sets, "channel_id = ?")
			args = append(args, *req.ChannelID)
		}
	}
	if req.Status != nil {
		if !validEventStatuses[*req.Status] {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		sets = append(sets, "status = ?")
		args = append(args, *req.Status)
	}

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, eventID)

	query := "UPDATE calendar_events SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := wdb.DB.Exec(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update event")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	e, _ := scanEvent(wdb.DB, eventID)

	_ = claims

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventUpdated, e), "")

	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
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

	// Also delete exception instances if this is a recurring parent
	_, _ = wdb.DB.Exec("DELETE FROM calendar_events WHERE recurrence_parent_id = ?", eventID)

	res, err := wdb.DB.Exec("DELETE FROM calendar_events WHERE id = ?", eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete event")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	_ = claims

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeEventDeleted, hub.EventDeletedPayload{ID: eventID}), "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// scanEvent reads a single event from the database.
func scanEvent(db *sql.DB, eventID string) (hub.EventPayload, error) {
	var e hub.EventPayload
	var attendeesStr, remindersStr string
	var allDay int
	err := db.QueryRow(
		`SELECT id, title, description, location, start_time, end_time, all_day, recurrence_rule, COALESCE(recurrence_parent_id,''), color, calendar, created_by, attendees, reminders, COALESCE(channel_id,''), status, created_at, updated_at
		 FROM calendar_events WHERE id = ?`, eventID,
	).Scan(&e.ID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &allDay, &e.RecurrenceRule,
		&e.RecurrenceParentID, &e.Color, &e.Calendar, &e.CreatedBy,
		&attendeesStr, &remindersStr, &e.ChannelID, &e.Status,
		&e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	e.AllDay = allDay == 1
	e.Attendees = json.RawMessage(attendeesStr)
	e.Reminders = json.RawMessage(remindersStr)
	return e, nil
}
