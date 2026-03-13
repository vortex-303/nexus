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
	"github.com/nexus-chat/nexus/internal/search"
)

type createTaskReq struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	ExpectedOutput string   `json:"expected_output"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	AssigneeID     string   `json:"assignee_id"`
	DueDate        string   `json:"due_date"`
	Tags           []string `json:"tags"`
	ChannelID      string   `json:"channel_id"`
	MessageID      string   `json:"message_id"`
	ScheduledAt    string   `json:"scheduled_at"`
	AgentID        string   `json:"agent_id"`
	RecurrenceRule string   `json:"recurrence_rule"`
}

type updateTaskReq struct {
	Title          *string  `json:"title"`
	Description    *string  `json:"description"`
	ExpectedOutput *string  `json:"expected_output"`
	Status         *string  `json:"status"`
	Priority       *string  `json:"priority"`
	AssigneeID     *string  `json:"assignee_id"`
	DueDate        *string  `json:"due_date"`
	Tags           []string `json:"tags"`
	Position       *int     `json:"position"`
	ScheduledAt    *string  `json:"scheduled_at"`
	AgentID        *string  `json:"agent_id"`
	RecurrenceRule *string  `json:"recurrence_rule"`
}

var validStatuses = map[string]bool{
	"backlog": true, "todo": true, "in_progress": true, "done": true, "cancelled": true,
}
var validPriorities = map[string]bool{
	"low": true, "medium": true, "high": true, "urgent": true,
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req createTaskReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title required")
		return
	}
	if req.Status == "" {
		req.Status = "backlog"
	}
	if !validStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if req.Priority == "" {
		req.Priority = "medium"
	}
	if !validPriorities[req.Priority] {
		writeError(w, http.StatusBadRequest, "invalid priority")
		return
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	taskID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	tagsJSON, _ := json.Marshal(req.Tags)

	// Auto-assign position: max position in same status + 1
	var maxPos int
	_ = wdb.DB.QueryRow("SELECT COALESCE(MAX(position), 0) FROM tasks WHERE status = ?", req.Status).Scan(&maxPos)
	position := maxPos + 1

	var dueDate sql.NullString
	if req.DueDate != "" {
		dueDate = sql.NullString{String: req.DueDate, Valid: true}
	}
	var assigneeID sql.NullString
	if req.AssigneeID != "" {
		assigneeID = sql.NullString{String: req.AssigneeID, Valid: true}
	}
	var channelID sql.NullString
	if req.ChannelID != "" {
		channelID = sql.NullString{String: req.ChannelID, Valid: true}
	}
	var messageID sql.NullString
	if req.MessageID != "" {
		messageID = sql.NullString{String: req.MessageID, Valid: true}
	}
	var scheduledAt sql.NullString
	if req.ScheduledAt != "" {
		scheduledAt = sql.NullString{String: req.ScheduledAt, Valid: true}
	}
	var agentID sql.NullString
	if req.AgentID != "" {
		agentID = sql.NullString{String: req.AgentID, Valid: true}
	}

	_, err = wdb.DB.Exec(
		`INSERT INTO tasks (id, title, description, expected_output, status, priority, assignee_id, created_by, due_date, tags, channel_id, message_id, position, scheduled_at, agent_id, recurrence_rule, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, req.Title, req.Description, req.ExpectedOutput, req.Status, req.Priority,
		assigneeID, claims.UserID, dueDate, string(tagsJSON),
		channelID, messageID, position, scheduledAt, agentID, req.RecurrenceRule, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create task")
		return
	}

	s.search.Index(slug, search.SearchDoc{
		ID: taskID, Type: "task", Title: req.Title, Content: req.Description,
		Status: req.Status, CreatedAt: now,
	})

	task := hub.TaskPayload{
		ID:             taskID,
		Title:          req.Title,
		Description:    req.Description,
		ExpectedOutput: req.ExpectedOutput,
		Status:         req.Status,
		Priority:       req.Priority,
		AssigneeID:     req.AssigneeID,
		CreatedBy:      claims.UserID,
		DueDate:        req.DueDate,
		Tags:           tagsJSON,
		ChannelID:      req.ChannelID,
		MessageID:      req.MessageID,
		Position:       position,
		ScheduledAt:    req.ScheduledAt,
		AgentID:        req.AgentID,
		RecurrenceRule: req.RecurrenceRule,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Broadcast to all workspace members
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskCreated, task), "")

	s.onPulse(slug, Pulse{
		Type: "task.created", ActorID: claims.UserID, ActorName: claims.DisplayName,
		EntityID: taskID, Summary: pulseSummary(claims.DisplayName, "created task", req.Title),
	})

	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
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

	// Optional filters
	status := r.URL.Query().Get("status")
	assignee := r.URL.Query().Get("assignee")
	priority := r.URL.Query().Get("priority")

	// Resolve "me" shorthand to current user ID
	if assignee == "me" {
		assignee = claims.UserID
	}

	query := "SELECT id, title, description, expected_output, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), position, COALESCE(scheduled_at,''), COALESCE(agent_id,''), recurrence_rule, created_at, updated_at FROM tasks WHERE 1=1"
	var args []any

	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if assignee != "" {
		query += " AND assignee_id = ?"
		args = append(args, assignee)
	}
	if priority != "" {
		query += " AND priority = ?"
		args = append(args, priority)
	}

	query += " ORDER BY position ASC, created_at DESC"

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	tasks := []hub.TaskPayload{}
	for rows.Next() {
		var t hub.TaskPayload
		var tagsStr string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.ExpectedOutput, &t.Status, &t.Priority,
			&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
			&t.ChannelID, &t.MessageID, &t.Position,
			&t.ScheduledAt, &t.AgentID, &t.RecurrenceRule,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		t.Tags = json.RawMessage(tagsStr)
		tasks = append(tasks, t)
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	taskID := r.PathValue("taskID")
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

	var t hub.TaskPayload
	var tagsStr string
	err = wdb.DB.QueryRow(
		"SELECT id, title, description, expected_output, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), position, COALESCE(scheduled_at,''), COALESCE(agent_id,''), recurrence_rule, created_at, updated_at FROM tasks WHERE id = ?",
		taskID,
	).Scan(&t.ID, &t.Title, &t.Description, &t.ExpectedOutput, &t.Status, &t.Priority,
		&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
		&t.ChannelID, &t.MessageID, &t.Position,
		&t.ScheduledAt, &t.AgentID, &t.RecurrenceRule,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	t.Tags = json.RawMessage(tagsStr)

	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	taskID := r.PathValue("taskID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req updateTaskReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Build dynamic update
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
	if req.ExpectedOutput != nil {
		sets = append(sets, "expected_output = ?")
		args = append(args, *req.ExpectedOutput)
	}
	if req.Status != nil {
		if !validStatuses[*req.Status] {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		sets = append(sets, "status = ?")
		args = append(args, *req.Status)
	}
	if req.Priority != nil {
		if !validPriorities[*req.Priority] {
			writeError(w, http.StatusBadRequest, "invalid priority")
			return
		}
		sets = append(sets, "priority = ?")
		args = append(args, *req.Priority)
	}
	if req.AssigneeID != nil {
		if *req.AssigneeID == "" {
			sets = append(sets, "assignee_id = NULL")
		} else {
			sets = append(sets, "assignee_id = ?")
			args = append(args, *req.AssigneeID)
		}
	}
	if req.DueDate != nil {
		if *req.DueDate == "" {
			sets = append(sets, "due_date = NULL")
		} else {
			sets = append(sets, "due_date = ?")
			args = append(args, *req.DueDate)
		}
	}
	if req.Tags != nil {
		tagsJSON, _ := json.Marshal(req.Tags)
		sets = append(sets, "tags = ?")
		args = append(args, string(tagsJSON))
	}
	if req.Position != nil {
		sets = append(sets, "position = ?")
		args = append(args, *req.Position)
	}
	if req.ScheduledAt != nil {
		if *req.ScheduledAt == "" {
			sets = append(sets, "scheduled_at = NULL")
		} else {
			sets = append(sets, "scheduled_at = ?")
			args = append(args, *req.ScheduledAt)
		}
	}
	if req.AgentID != nil {
		if *req.AgentID == "" {
			sets = append(sets, "agent_id = NULL")
		} else {
			sets = append(sets, "agent_id = ?")
			args = append(args, *req.AgentID)
		}
	}
	if req.RecurrenceRule != nil {
		sets = append(sets, "recurrence_rule = ?")
		args = append(args, *req.RecurrenceRule)
	}

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, taskID)

	query := "UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := wdb.DB.Exec(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update task")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	// Read back updated task
	var t hub.TaskPayload
	var tagsStr string
	_ = wdb.DB.QueryRow(
		"SELECT id, title, description, expected_output, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), position, COALESCE(scheduled_at,''), COALESCE(agent_id,''), recurrence_rule, created_at, updated_at FROM tasks WHERE id = ?",
		taskID,
	).Scan(&t.ID, &t.Title, &t.Description, &t.ExpectedOutput, &t.Status, &t.Priority,
		&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
		&t.ChannelID, &t.MessageID, &t.Position,
		&t.ScheduledAt, &t.AgentID, &t.RecurrenceRule,
		&t.CreatedAt, &t.UpdatedAt)
	t.Tags = json.RawMessage(tagsStr)

	s.search.Index(slug, search.SearchDoc{
		ID: t.ID, Type: "task", Title: t.Title, Content: t.Description, Status: t.Status,
	})

	_ = claims // used for auth check above

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskUpdated, t), "")

	pulseType := "task.updated"
	verb := "updated task"
	if req.Status != nil && *req.Status == "done" {
		pulseType = "task.completed"
		verb = "completed"
	}
	s.onPulse(slug, Pulse{
		Type: pulseType, ActorID: claims.UserID, ActorName: claims.DisplayName,
		EntityID: taskID, Summary: pulseSummary(claims.DisplayName, verb, t.Title),
	})

	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	taskID := r.PathValue("taskID")
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

	var taskTitle string
	_ = wdb.DB.QueryRow("SELECT title FROM tasks WHERE id = ?", taskID).Scan(&taskTitle)

	res, err := wdb.DB.Exec("DELETE FROM tasks WHERE id = ?", taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete task")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	s.search.Delete(slug, taskID)

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskDeleted, hub.TaskDeletedPayload{ID: taskID}), "")

	s.onPulse(slug, Pulse{
		Type: "task.deleted", ActorID: claims.UserID, ActorName: claims.DisplayName,
		EntityID: taskID, Summary: pulseSummary(claims.DisplayName, "deleted task", taskTitle),
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
