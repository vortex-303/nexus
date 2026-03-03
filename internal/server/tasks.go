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

type createTaskReq struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	AssigneeID  string   `json:"assignee_id"`
	DueDate     string   `json:"due_date"`
	Tags        []string `json:"tags"`
	ChannelID   string   `json:"channel_id"`
	MessageID   string   `json:"message_id"`
}

type updateTaskReq struct {
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Status      *string  `json:"status"`
	Priority    *string  `json:"priority"`
	AssigneeID  *string  `json:"assignee_id"`
	DueDate     *string  `json:"due_date"`
	Tags        []string `json:"tags"`
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

	_, err = wdb.DB.Exec(
		`INSERT INTO tasks (id, title, description, status, priority, assignee_id, created_by, due_date, tags, channel_id, message_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, req.Title, req.Description, req.Status, req.Priority,
		assigneeID, claims.UserID, dueDate, string(tagsJSON),
		channelID, messageID, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create task")
		return
	}

	task := hub.TaskPayload{
		ID:          taskID,
		Title:       req.Title,
		Description: req.Description,
		Status:      req.Status,
		Priority:    req.Priority,
		AssigneeID:  req.AssigneeID,
		CreatedBy:   claims.UserID,
		DueDate:     req.DueDate,
		Tags:        tagsJSON,
		ChannelID:   req.ChannelID,
		MessageID:   req.MessageID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Broadcast to all workspace members
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskCreated, task), "")

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

	query := "SELECT id, title, description, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), created_at, updated_at FROM tasks WHERE 1=1"
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

	query += " ORDER BY created_at DESC"

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
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
			&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
			&t.ChannelID, &t.MessageID, &t.CreatedAt, &t.UpdatedAt); err != nil {
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
		"SELECT id, title, description, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), created_at, updated_at FROM tasks WHERE id = ?",
		taskID,
	).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
		&t.ChannelID, &t.MessageID, &t.CreatedAt, &t.UpdatedAt)
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
		"SELECT id, title, description, status, priority, COALESCE(assignee_id,''), created_by, COALESCE(due_date,''), tags, COALESCE(channel_id,''), COALESCE(message_id,''), created_at, updated_at FROM tasks WHERE id = ?",
		taskID,
	).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.AssigneeID, &t.CreatedBy, &t.DueDate, &tagsStr,
		&t.ChannelID, &t.MessageID, &t.CreatedAt, &t.UpdatedAt)
	t.Tags = json.RawMessage(tagsStr)

	_ = claims // used for auth check above

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskUpdated, t), "")

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

	res, err := wdb.DB.Exec("DELETE FROM tasks WHERE id = ?", taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete task")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskDeleted, hub.TaskDeletedPayload{ID: taskID}), "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
