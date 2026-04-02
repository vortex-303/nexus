package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain2"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// executeTool runs a tool call and returns the result as a string.
// senderMemberID is the member who triggered the tool (for attribution). Empty = Brain.
func (s *Server) executeTool(slug, channelID, senderMemberID string, call brain.ToolCall) string {
	start := time.Now()
	result := s.executeToolInner(slug, channelID, senderMemberID, call)
	metrics.ToolLatency.WithLabelValues(call.Function.Name).Observe(time.Since(start).Seconds())
	status := "ok"
	if strings.HasPrefix(result, "Error") {
		status = "error"
	}
	metrics.ToolCallsTotal.WithLabelValues(call.Function.Name, status).Inc()
	return result
}

func (s *Server) executeToolInner(slug, channelID, senderMemberID string, call brain.ToolCall) string {
	switch call.Function.Name {
	case "create_task":
		return s.toolCreateTask(slug, channelID, call.Function.Arguments)
	case "list_tasks":
		return s.toolListTasks(slug, call.Function.Arguments)
	case "update_task":
		return s.toolUpdateTask(slug, call.Function.Arguments)
	case "delete_task":
		return s.toolDeleteTask(slug, call.Function.Arguments)
	case "search_workspace", "search_messages":
		return s.toolSearchMessages(slug, channelID, call.Function.Arguments)
	case "create_document":
		return s.toolCreateDocument(slug, call.Function.Arguments)
	case "search_knowledge":
		return s.toolSearchKnowledge(slug, call.Function.Arguments)
	case "delegate_to_agent":
		return s.handleDelegateToAgent(slug, channelID, call.Function.Arguments)
	case "ask_agent":
		return s.handleAskAgent(slug, channelID, call.Function.Arguments)
	case "recall_memory":
		return s.toolRecallMemory(slug, call.Function.Arguments)
	case "save_memory":
		return s.toolSaveMemory(slug, channelID, call.Function.Arguments)
	case "generate_image":
		return s.toolGenerateImage(slug, channelID, call.Function.Arguments)
	case "create_calendar_event":
		return s.toolCreateCalendarEvent(slug, channelID, senderMemberID, call.Function.Arguments)
	case "list_calendar_events":
		return s.toolListCalendarEvents(slug, call.Function.Arguments)
	case "update_calendar_event":
		return s.toolUpdateCalendarEvent(slug, call.Function.Arguments)
	case "delete_calendar_event":
		return s.toolDeleteCalendarEvent(slug, call.Function.Arguments)
	case "send_email":
		return s.toolSendEmail(slug, call.Function.Arguments)
	case "send_telegram":
		return s.toolSendTelegram(slug, channelID, call.Function.Arguments)
	case "web_search":
		return s.toolWebSearch(slug, call.Function.Arguments)
	case "search_x":
		return s.toolSearchX(slug, call.Function.Arguments)
	case "fetch_url":
		return toolFetchURL(call.Function.Arguments)
	case "trace_knowledge":
		return s.toolTraceKnowledge(slug, call.Function.Arguments)
	default:
		// Route to MCP server
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		mgr := s.getMCPManager(slug)
		var args map[string]any
		json.Unmarshal([]byte(call.Function.Arguments), &args)
		result, err := mgr.CallTool(ctx, call.Function.Name, args)
		if err != nil {
			return fmt.Sprintf("MCP tool error: %s", err)
		}
		return result
	}
}

func (s *Server) toolCreateTask(slug, channelID, argsJSON string) string {
	var args struct {
		Title          string `json:"title"`
		Description    string `json:"description"`
		ExpectedOutput string `json:"expected_output"`
		Status         string `json:"status"`
		Priority       string `json:"priority"`
		AssigneeName   string `json:"assignee_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	if args.Status == "" {
		args.Status = "todo"
	}
	if args.Priority == "" {
		args.Priority = "medium"
	}

	// Resolve assignee name to ID
	var assigneeID string
	if args.AssigneeName != "" {
		_ = wdb.DB.QueryRow(
			"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)",
			args.AssigneeName,
		).Scan(&assigneeID)
	}

	taskID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		`INSERT INTO tasks (id, title, description, expected_output, status, priority, assignee_id, created_by, channel_id, tags, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?)`,
		taskID, args.Title, args.Description, args.ExpectedOutput, args.Status, args.Priority,
		assigneeID, brain.BrainMemberID, channelID, now, now,
	)
	if err != nil {
		return "Error creating task: " + err.Error()
	}

	// Broadcast task.created
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskCreated, map[string]any{
		"id": taskID, "title": args.Title, "description": args.Description,
		"expected_output": args.ExpectedOutput,
		"status": args.Status, "priority": args.Priority,
		"assignee_id": assigneeID, "created_by": brain.BrainMemberID,
		"tags": json.RawMessage("[]"), "created_at": now, "updated_at": now,
	}), "")

	result := fmt.Sprintf("Task created: \"%s\" [%s, %s]", args.Title, args.Status, args.Priority)
	if assigneeID != "" {
		result += fmt.Sprintf(" assigned to %s", args.AssigneeName)
	}
	return result
}

func (s *Server) toolListTasks(slug, argsJSON string) string {
	var args struct {
		Status       string `json:"status"`
		AssigneeName string `json:"assignee_name"`
	}
	if argsJSON != "" {
		json.Unmarshal([]byte(argsJSON), &args)
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	query := `SELECT t.id, t.title, t.status, t.priority, COALESCE(m.display_name, ''), t.due_date
		FROM tasks t LEFT JOIN members m ON m.id = t.assignee_id WHERE 1=1`
	var qargs []any

	if args.Status != "" {
		query += " AND t.status = ?"
		qargs = append(qargs, args.Status)
	}
	if args.AssigneeName != "" {
		query += " AND LOWER(m.display_name) = LOWER(?)"
		qargs = append(qargs, args.AssigneeName)
	}
	query += " ORDER BY t.created_at DESC LIMIT 20"

	rows, err := wdb.DB.Query(query, qargs...)
	if err != nil {
		return "Error querying tasks"
	}
	defer rows.Close()

	var lines []string
	count := 0
	for rows.Next() {
		var tid, title, status, priority, assignee, dueDate string
		rows.Scan(&tid, &title, &status, &priority, &assignee, &dueDate)
		line := fmt.Sprintf("- [%s] %s (%s) (id: %s)", status, title, priority, tid)
		if assignee != "" {
			line += " → " + assignee
		}
		if dueDate != "" {
			line += " due:" + dueDate
		}
		lines = append(lines, line)
		count++
	}

	if count == 0 {
		return "No tasks found matching the criteria."
	}
	return fmt.Sprintf("%d tasks found:\n%s", count, strings.Join(lines, "\n"))
}

func (s *Server) toolUpdateTask(slug, argsJSON string) string {
	var args struct {
		TaskID       string `json:"task_id"`
		Title        string `json:"title"`
		Status       string `json:"status"`
		Priority     string `json:"priority"`
		AssigneeName string `json:"assignee_name"`
		DueDate      string `json:"due_date"`
		Description  string `json:"description"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.TaskID == "" {
		return "Error: task_id is required"
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
	if args.Description != "" {
		sets = append(sets, "description = ?")
		qargs = append(qargs, args.Description)
	}
	if args.Status != "" {
		sets = append(sets, "status = ?")
		qargs = append(qargs, args.Status)
	}
	if args.Priority != "" {
		sets = append(sets, "priority = ?")
		qargs = append(qargs, args.Priority)
	}
	if args.DueDate != "" {
		sets = append(sets, "due_date = ?")
		qargs = append(qargs, args.DueDate)
	} else {
		// Check if due_date was explicitly set to empty string (clear it)
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(argsJSON), &raw) == nil {
			if v, ok := raw["due_date"]; ok && string(v) == `""` {
				sets = append(sets, "due_date = NULL")
			}
		}
	}
	if args.AssigneeName != "" {
		var assigneeID string
		_ = wdb.DB.QueryRow(
			"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)",
			args.AssigneeName,
		).Scan(&assigneeID)
		if assigneeID != "" {
			sets = append(sets, "assignee_id = ?")
			qargs = append(qargs, assigneeID)
		}
	} else {
		// Check if assignee_name was explicitly set to empty string (unassign)
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(argsJSON), &raw) == nil {
			if v, ok := raw["assignee_name"]; ok && string(v) == `""` {
				sets = append(sets, "assignee_id = NULL")
			}
		}
	}

	if len(sets) == 0 {
		return "Error: no fields to update"
	}

	sets = append(sets, "updated_at = ?")
	qargs = append(qargs, now)
	qargs = append(qargs, args.TaskID)

	query := "UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := wdb.DB.Exec(query, qargs...)
	if err != nil {
		return "Error updating task: " + err.Error()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Task not found"
	}

	// Read back updated task
	t, _ := scanTask(wdb.DB.QueryRow(
		"SELECT "+taskSelectCols+" FROM tasks WHERE id = ?", args.TaskID,
	))

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskUpdated, t), "")

	return fmt.Sprintf("Task updated: \"%s\" → %s", t.Title, t.Status)
}

func (s *Server) toolDeleteTask(slug, argsJSON string) string {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.TaskID == "" {
		return "Error: task_id is required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Get title before deleting
	var title string
	_ = wdb.DB.QueryRow("SELECT title FROM tasks WHERE id = ?", args.TaskID).Scan(&title)

	res, err := wdb.DB.Exec("DELETE FROM tasks WHERE id = ?", args.TaskID)
	if err != nil {
		return "Error deleting task: " + err.Error()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "Task not found"
	}

	s.search.Delete(slug, args.TaskID)

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskDeleted, hub.TaskDeletedPayload{ID: args.TaskID}), "")

	return fmt.Sprintf("Task deleted: \"%s\"", title)
}

func (s *Server) toolSearchMessages(slug, channelID, argsJSON string) string {
	var args struct {
		Query       string `json:"query"`
		ChannelName string `json:"channel_name"`
		SenderName  string `json:"sender_name"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 10
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Build SQL query with optional filters
	query := `
		SELECT m.content, m.created_at,
			COALESCE(mem.display_name, m.sender_id) as sender_name,
			COALESCE(c.name, '') as channel_name
		FROM messages m
		LEFT JOIN members mem ON mem.id = m.sender_id
		LEFT JOIN channels c ON c.id = m.channel_id
		WHERE m.deleted = FALSE AND m.content LIKE ?`
	params := []any{"%" + args.Query + "%"}

	if args.ChannelName != "" {
		query += " AND LOWER(c.name) = LOWER(?)"
		params = append(params, args.ChannelName)
	}
	if args.SenderName != "" {
		query += " AND LOWER(mem.display_name) LIKE LOWER(?)"
		params = append(params, "%"+args.SenderName+"%")
	}

	query += " ORDER BY m.created_at DESC LIMIT ?"
	params = append(params, args.Limit)

	rows, err := wdb.DB.Query(query, params...)
	if err != nil {
		return "Error searching messages"
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var content, createdAt, senderName, channelName string
		if err := rows.Scan(&content, &createdAt, &senderName, &channelName); err != nil {
			continue
		}
		if len(content) > 300 {
			content = content[:297] + "..."
		}
		// Format timestamp nicely
		ts := createdAt
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			ts = t.Format("Jan 2, 3:04 PM")
		}
		lines = append(lines, fmt.Sprintf("[#%s] %s (%s): %s", channelName, senderName, ts, content))
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No messages found matching \"%s\"", args.Query)
	}
	return fmt.Sprintf("Found %d messages:\n%s", len(lines), strings.Join(lines, "\n"))
}

func (s *Server) toolCreateDocument(slug, argsJSON string) string {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	docID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		"INSERT INTO documents (id, title, content, created_by, updated_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		docID, args.Title, args.Content, brain.BrainMemberID, brain.BrainMemberID, now, now,
	)
	if err != nil {
		return "Error creating document: " + err.Error()
	}

	// Broadcast doc.created
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope("doc.created", map[string]any{
		"id": docID, "title": args.Title, "content": args.Content,
		"created_by": brain.BrainMemberID, "updated_by": brain.BrainMemberID,
		"sharing": "workspace", "created_at": now, "updated_at": now,
	}), "")

	return fmt.Sprintf("Document created: \"%s\"", args.Title)
}

func (s *Server) toolSearchKnowledge(slug, argsJSON string) string {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	apiKey, _ := s.getBrainSettings(slug)
	return brain.SearchKnowledgeForTool(wdb.DB, args.Query, brain.SemanticOpts{
		VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
	})
}

// resolveMemberIDByName looks up a member ID by display name.
func (s *Server) resolveMemberIDByName(slug, name string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var memberID string
	_ = wdb.DB.QueryRow(
		"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)", name,
	).Scan(&memberID)
	return memberID
}

// toolRecallMemory searches workspace memory for specific facts, decisions, or commitments.
func (s *Server) toolRecallMemory(slug, argsJSON string) string {
	var args struct {
		Query             string `json:"query"`
		Type              string `json:"type"`
		IncludeSuperseded bool   `json:"include_superseded"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}
	if args.Query == "" {
		return "Error: query is required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	memories, err := brain.SearchMemoriesTyped(wdb.DB, args.Query, args.Type, 10)
	if err != nil || len(memories) == 0 {
		return "No memories found matching: " + args.Query
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Found %d memories:", len(memories)))
	for _, m := range memories {
		if !args.IncludeSuperseded && (m.SupersededBy != "" || m.ValidUntil != "") {
			continue
		}
		summary := m.Summary
		if summary == "" {
			summary = m.Content
		}
		line := fmt.Sprintf("- [%s] %s (confidence: %.1f)", m.Type, summary, m.Confidence)
		if m.Participants != "" {
			line += fmt.Sprintf(" — %s", m.Participants)
		}
		if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
			line += fmt.Sprintf(" (%s)", t.Format("Jan 2, 2006"))
		}
		if m.SupersededBy != "" {
			line += " [SUPERSEDED]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// toolSaveMemory saves an important fact, decision, or commitment to workspace memory.
func (s *Server) toolSaveMemory(slug, channelID, argsJSON string) string {
	var args struct {
		Content      string  `json:"content"`
		Type         string  `json:"type"`
		Importance   float64 `json:"importance"`
		Participants string  `json:"participants"`
		Metadata     string  `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}
	if args.Content == "" {
		return "Error: content is required"
	}
	if args.Type == "" {
		args.Type = brain.MemoryTypeDecision
	}
	if args.Importance <= 0 {
		args.Importance = 0.8
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	if brain.MemorySimilarExists(wdb.DB, args.Type, args.Content) {
		return "Memory already exists: " + args.Content
	}

	if err := brain.SaveMemoryFull(wdb.DB, id.New(), args.Type, args.Content, "agent", channelID, "", args.Importance, "", 0.8, args.Participants); err != nil {
		return "Error saving memory: " + err.Error()
	}

	return fmt.Sprintf("Memory saved: [%s] %s", args.Type, args.Content)
}

// findToolDef looks up a tool definition by name.
func findToolDef(tools []brain.ToolDef, name string) *brain.ToolDef {
	for i := range tools {
		if tools[i].Function.Name == name {
			return &tools[i]
		}
	}
	return nil
}

// handleBrainMentionWithTools processes a @Brain mention with tool support.
// modelOverride, if non-empty, replaces the workspace default model.
// TaskCompletionCallback is called when a task-triggered LLM run finishes.
type TaskCompletionCallback func(msgID, response string, err error)

func (s *Server) handleBrainMentionWithTools(slug, channelID, parentID, senderName, content string, messageTime time.Time, modelOverride ...string) {
	s.handleBrainMentionWithToolsEx(slug, channelID, parentID, senderName, content, messageTime, nil, modelOverride...)
}

func (s *Server) handleBrainMentionWithToolsEx(slug, channelID, parentID, senderName, content string, messageTime time.Time, onComplete TaskCompletionCallback, modelOverride ...string) {
	go func() {
		// Completion tracking for task scheduler callbacks
		var completionMsgID, completionResponse string
		var completionErr error
		defer func() {
			if onComplete != nil {
				onComplete(completionMsgID, completionResponse, completionErr)
			}
		}()

		// Acquire semaphore to limit concurrent brain goroutines
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("too many concurrent agents, queuing")
			s.agentSem <- struct{}{} // block until slot available
			defer func() { <-s.agentSem }()
		}
		// Skip messages from before this server boot (e.g., after restart)
		if messageTime.Before(s.bootedAt) {
			logger.WithCategory(logger.CatBrain).Info().Time("msg_time", messageTime).Time("boot", s.bootedAt).Msg("skipping pre-boot message")
			return
		}
		// Tiered staleness: threads are faster-paced than channel mentions
		maxAge := maxBrainChannelAge
		if parentID != "" {
			maxAge = maxBrainThreadAge
		}
		if time.Since(messageTime) > maxAge {
			logger.WithCategory(logger.CatBrain).Info().Dur("age", time.Since(messageTime)).Dur("max", maxAge).Msg("skipping stale message")
			return
		}
		metrics.AgentExecutionsTotal.WithLabelValues("Brain", "started").Inc()

		// Handle /search, /localsearch, and natural language web search directly (bypass LLM tool selection)
		trimmed := strings.TrimSpace(content)
		if webQuery := extractWebSearchQuery(trimmed); webQuery != "" {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", "web_search", parentID)
			argsJSON := fmt.Sprintf(`{"query":%q}`, webQuery)
			result := s.toolWebSearch(slug, argsJSON)
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, result, "web_search")
			completionResponse = result
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)
			return
		}
		if strings.HasPrefix(trimmed, "/localsearch ") {
			query := strings.TrimSpace(strings.TrimPrefix(trimmed, "/localsearch"))
			if query != "" {
				s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
				s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", "search_workspace", parentID)
				argsJSON := fmt.Sprintf(`{"query":%q}`, query)
				result := s.toolSearchMessages(slug, channelID, argsJSON)
				completionMsgID = s.sendBrainMessage(slug, channelID, parentID, result, "search_workspace")
				completionResponse = result
				s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)
				return
			}
		}

		// Standard chat pre-filter disabled — all messages go to LLM
		// TODO: re-enable when standard chat patterns are production-ready
		// if s.getBrainSetting(slug, "standard_chat_enabled", "true") != "false" {
		// 	if wdb, err := s.ws.Open(slug); err == nil {
		// 		if response, handled := s.tryZeroLLMResponse(slug, content, wdb.DB, senderName); handled {
		// 			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		// 			s.sendBrainMessage(slug, channelID, parentID, response)
		// 			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")
		// 			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
		// 				truncate(content, 200), truncate(response, 500), "zero-llm", nil)
		// 			return
		// 		}
		// 	}
		// }

		// LLM-disabled gate removed — makeBrainClient handles xAI/OpenRouter routing,
		// and the apiKey+xaiKey check below handles the no-key case.

		apiKey, model := s.getBrainSettings(slug)
		if len(modelOverride) > 0 && modelOverride[0] != "" {
			model = modelOverride[0]
		}
		ollamaEnabled := s.getBrainSetting(slug, "ollama_enabled") == "true"
		if apiKey == "" && s.getXAIKey(slug) == "" && !ollamaEnabled {
			errMsg := "I can answer search and stats queries without an API key. Try: \"search for X\", \"how many messages\", \"who is online\", \"list channels\". For general questions, configure an API key in Settings."
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, errMsg)
			completionResponse = errMsg
			completionErr = fmt.Errorf("no API key configured")
			return
		}

		// Broadcast thinking state (scoped to thread if parentID set)
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("failed to build system prompt")
			return
		}
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(systemPrompt)).Msg("base prompt")

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("workspace error")
			return
		}

		// Build context based on attention mode (thread vs channel)
		systemPrompt = s.buildContextForMode(slug, wdb, channelID, parentID, content, senderName, apiKey, brainDir, systemPrompt)

		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", len(messages)).Msg("messages")

		// Resolve free-auto virtual model
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)

		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		// First call: with tools (built-in + MCP)
		allTools := s.getAllTools(slug)
		responseContent, toolCalls, usage, err := client.CompleteWithTools(systemPrompt, messages, allTools)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("LLM error")
			errMsg := "Sorry, I encountered an error. Check that the API key is configured correctly."
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, errMsg)
			completionResponse = errMsg
			completionErr = err
			return
		}
		s.trackUsage(slug, usage, resolvedModel, "tools", channelID, senderName)

		// If no tool calls, just send the text response
		if len(toolCalls) == 0 {
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				completionMsgID = s.sendBrainMessage(slug, channelID, parentID, responseContent)
			}
			completionResponse = responseContent
			// Log the action
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			return
		}

		// Execute tool calls and build follow-up messages
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", len(toolCalls)).Msg("executing tool calls")

		// Add the assistant's tool-call message
		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var imageRefs []string
		var toolResults []string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name, parentID)
			senderID := s.resolveMemberIDByName(slug, senderName)
			result := s.executeTool(slug, channelID, senderID, call)
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("tool", call.Function.Name).Str("result", truncate(result, 100)).Msg("tool result")

			// Extract image markdown from tool results so the follow-up LLM can't drop it
			imageRefs = append(imageRefs, extractImageMarkdown(result)...)
			toolResults = append(toolResults, result)

			// Add tool result with tool_call_id
			followUp = append(followUp, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		// Check if a single tool with ResultAsAnswer was called — skip second LLM round
		if len(toolCalls) == 1 {
			if td := findToolDef(allTools, toolCalls[0].Function.Name); td != nil && td.Function.ResultAsAnswer {
				finalResponse := appendMissingImages(toolResults[0], imageRefs)
				var toolNames []string
				for _, call := range toolCalls {
					toolNames = append(toolNames, call.Function.Name)
				}
				if finalResponse != "" {
					completionMsgID = s.sendBrainMessage(slug, channelID, parentID, finalResponse, toolNames...)
				}
				completionResponse = finalResponse
				brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
					truncate(content, 200), truncate(finalResponse, 500), model, toolNames)
				return
			}
		}

		// Second call: get final response incorporating tool results
		finalResponse, usage2, err := client.Complete(systemPrompt, followUp)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("follow-up LLM error")
			completionErr = err
			// Fall back to the initial response if any
			if responseContent != "" {
				completionMsgID = s.sendBrainMessage(slug, channelID, parentID, appendMissingImages(responseContent, imageRefs))
				completionResponse = responseContent
			}
			return
		}
		s.trackUsage(slug, usage2, resolvedModel, "tools", channelID, senderName)

		finalResponse = strings.TrimSpace(finalResponse)
		// Append any image markdown that the LLM may have omitted
		finalResponse = appendMissingImages(finalResponse, imageRefs)

		// Collect tool names for metadata
		var toolNames []string
		for _, call := range toolCalls {
			toolNames = append(toolNames, call.Function.Name)
		}

		if finalResponse != "" {
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, finalResponse, toolNames...)
		}
		completionResponse = finalResponse

		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(finalResponse, 500), model, toolNames)
	}()
}

// buildContextForMode assembles the system prompt context based on attention mode.
// Thread mode (parentID set) uses a focused context; channel mode uses full workspace awareness.
func (s *Server) buildContextForMode(slug string, wdb *db.WorkspaceDB, channelID, parentID, content, senderName, apiKey, brainDir, systemPrompt string) string {
	// Inject North Star context (always)
	if nsCtx := s.buildNorthStarContext(slug); nsCtx != "" {
		systemPrompt += "\n\n---\n\n" + nsCtx
	}

	if parentID != "" {
		// === THREAD MODE: focused context, let thread messages dominate ===

		// Ensure thread context row exists and get topic
		s.ensureThreadContext(wdb, channelID, parentID)
		var topic string
		var participantCount, messageCount int
		_ = wdb.DB.QueryRow(
			"SELECT topic, participant_count, message_count FROM thread_context WHERE parent_id = ?",
			parentID,
		).Scan(&topic, &participantCount, &messageCount)

		if topic != "" {
			systemPrompt += fmt.Sprintf("\n\n## Current Thread\nThread topic: %s\nParticipants: %d | Messages: %d\n",
				topic, participantCount, messageCount)
		}

		// Tell Brain who it's talking to
		systemPrompt += fmt.Sprintf("\n\n## Current Conversation\nYou are talking to: **%s**. Address them by this name.", senderName)
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("mode", "thread").Int("chars", len(systemPrompt)).Msg("thread context")

		// WARM: Memories (query-filtered to content)
		memoryContext := brain.BuildMemoryContext(wdb.DB, content)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(memoryContext)).Int("total", len(systemPrompt)).Msg("+memories (thread)")
		}

		// WARM: Channel summary (capped at 500 chars)
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			if len(chSummary) > 500 {
				chSummary = chSummary[:500] + "\n[...]"
			}
			systemPrompt += "\n\n---\n\n" + chSummary
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(chSummary)).Int("total", len(systemPrompt)).Msg("+channel_summary (thread, capped)")
		}

		// Skills (still include — they define capabilities)
		skills := brain.LoadSkills(brainDir)
		s.applySkillEnabledState(slug, skills)
		var senderRole string
		_ = wdb.DB.QueryRow("SELECT role FROM members WHERE LOWER(display_name) = LOWER(?)", senderName).Scan(&senderRole)
		skills = brain.FilterSkillsByRole(skills, senderRole)
		skills = filterEnabledSkills(skills)
		skillContext := brain.BuildSkillContext(skills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(skillContext)).Int("total", len(systemPrompt)).Msg("+skills (thread)")
		}

		// COLD: Knowledge — only if topic-relevant, capped at 5000 chars
		kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
			VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
		})
		if kbContext != "" {
			if len(kbContext) > 5000 {
				kbContext = kbContext[:5000] + "\n[...]"
			}
			systemPrompt += "\n\n---\n\n" + kbContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(kbContext)).Int("total", len(systemPrompt)).Msg("+knowledge (thread, capped)")
		}

		// SKIP: workspace snapshot, cross-channel context (Brain has tools if needed)

	} else {
		// === CHANNEL MODE: full workspace awareness (existing behavior) ===

		// Append workspace snapshot
		wsContext := brain.BuildWorkspaceContext(wdb.DB)
		if wsContext != "" {
			systemPrompt += "\n\n---\n\n" + wsContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(wsContext)).Int("total", len(systemPrompt)).Msg("+workspace_context")
		}

		// Tell Brain who it's talking to
		systemPrompt += fmt.Sprintf("\n\n## Current Conversation\nYou are talking to: **%s**. Address them by this name.", senderName)

		// Append memories
		memoryContext := brain.BuildMemoryContext(wdb.DB, content)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(memoryContext)).Int("total", len(systemPrompt)).Msg("+memories")
		}

		// Append skills context (role-gated + enabled filter)
		skills := brain.LoadSkills(brainDir)
		s.applySkillEnabledState(slug, skills)
		var senderRole string
		_ = wdb.DB.QueryRow("SELECT role FROM members WHERE LOWER(display_name) = LOWER(?)", senderName).Scan(&senderRole)
		skills = brain.FilterSkillsByRole(skills, senderRole)
		skills = filterEnabledSkills(skills)
		skillContext := brain.BuildSkillContext(skills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(skillContext)).Int("total", len(systemPrompt)).Msg("+skills")
		}

		// Append knowledge base context
		kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
			VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
		})
		if kbContext != "" {
			systemPrompt += "\n\n---\n\n" + kbContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(kbContext)).Int("total", len(systemPrompt)).Msg("+knowledge")
		}

		// Append channel history summary
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(chSummary)).Int("total", len(systemPrompt)).Msg("+channel_summary")
		}

		// Append cross-channel awareness (Brain only)
		if crossCtx := brain.BuildCrossChannelContext(wdb.DB, channelID); crossCtx != "" {
			systemPrompt += "\n\n---\n\n" + crossCtx
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(crossCtx)).Int("total", len(systemPrompt)).Msg("+cross_channel")
		}
	}

	// Hard cap
	if len(systemPrompt) > 100000 {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Int("chars", len(systemPrompt)).Msg("system prompt too large, truncating to 100000")
		systemPrompt = systemPrompt[:100000]
	}

	return systemPrompt
}

// extractWebSearchQuery detects web search intent and returns the query, or "" if not a web search.
// Matches: /search <query>, "search the web for <query>", "web search <query>", "look up <query> online", etc.
var webSearchRe = regexp.MustCompile(`(?i)^(?:/search\s+(.+)|(?:search|look\s+up|find)\s+(?:the\s+)?(?:web|internet|online)\s+(?:for\s+)?(.+)|(?:web\s+search|google)\s+(?:for\s+)?(.+)|(?:search|look\s+up|find)\s+(?:for\s+)?(.+?)(?:\s+(?:on\s+(?:the\s+)?(?:web|internet|google)|online))$)`)

func extractWebSearchQuery(s string) string {
	// /search command
	if strings.HasPrefix(s, "/search ") {
		return strings.TrimSpace(strings.TrimPrefix(s, "/search"))
	}

	m := webSearchRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	for i := 1; i < len(m); i++ {
		if m[i] != "" {
			return strings.TrimSpace(m[i])
		}
	}
	return ""
}

// filterEnabledSkills returns only skills where Enabled is true.
func filterEnabledSkills(skills []brain.Skill) []brain.Skill {
	var out []brain.Skill
	for _, sk := range skills {
		if sk.Enabled {
			out = append(out, sk)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// resolveAssigneeID looks up a member ID by display name.
func resolveAssigneeID(wdb *db.WorkspaceDB, name string) string {
	var id string
	_ = wdb.DB.QueryRow(
		"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)", name,
	).Scan(&id)
	return id
}

// extractImageMarkdown pulls out all ![...](/api/workspaces/...) references from a tool result.
// These are appended directly to the final message so the follow-up LLM can't drop them.
func extractImageMarkdown(s string) []string {
	var refs []string
	for {
		idx := strings.Index(s, "![")
		if idx < 0 {
			break
		}
		closeParen := strings.Index(s[idx:], ")")
		if closeParen < 0 {
			break
		}
		md := s[idx : idx+closeParen+1]
		if strings.Contains(md, "/api/workspaces/") {
			refs = append(refs, md)
		}
		s = s[idx+closeParen+1:]
	}
	return refs
}

// extractImagePromptTag strips <image-prompt>...</image-prompt> from a string and returns both.
func extractImagePromptTag(s string) (cleaned string, tag string) {
	start := strings.Index(s, "<image-prompt>")
	if start < 0 {
		return s, ""
	}
	end := strings.Index(s, "</image-prompt>")
	if end < 0 {
		return s, ""
	}
	end += len("</image-prompt>")
	tag = "\n\n" + strings.TrimSpace(s[start:end])
	cleaned = strings.TrimSpace(s[:start] + s[end:])
	return cleaned, tag
}

// appendMissingImages appends image markdown refs whose URLs are not already present in the response.
func appendMissingImages(response string, imageRefs []string) string {
	// First, strip any LLM-fabricated image refs that don't match our known refs
	response = stripFakeImageRefs(response, imageRefs)

	for _, ref := range imageRefs {
		// Extract just the URL from ![alt](URL) to compare — LLM may use different alt text
		url := extractURLFromImageMd(ref)
		if url != "" && strings.Contains(response, url) {
			continue // URL already present, skip
		}
		response += "\n\n" + ref
	}
	return response
}

// stripFakeImageRefs removes any ![...](...) refs pointing to /api/workspaces/ that aren't in our known set.
// The follow-up LLM sometimes fabricates plausible-looking image URLs that 404.
func stripFakeImageRefs(response string, knownRefs []string) string {
	knownURLs := map[string]bool{}
	for _, ref := range knownRefs {
		if u := extractURLFromImageMd(ref); u != "" {
			knownURLs[u] = true
		}
	}
	if len(knownURLs) == 0 {
		return response
	}

	// Extract all image refs from the response
	fakeRefs := extractImageMarkdown(response)
	for _, ref := range fakeRefs {
		url := extractURLFromImageMd(ref)
		if url != "" && !knownURLs[url] {
			// This is a fabricated ref — remove it from the response
			response = strings.Replace(response, ref, "", 1)
		}
	}
	return strings.TrimSpace(response)
}

// extractURLFromImageMd pulls the URL from a ![alt](url) markdown image reference.
func extractURLFromImageMd(md string) string {
	start := strings.Index(md, "](")
	if start < 0 {
		return ""
	}
	end := strings.Index(md[start:], ")")
	if end < 0 {
		return ""
	}
	return md[start+2 : start+end]
}

// PromptEnrichmentSystemPrompt instructs the LLM to craft a detailed image generation prompt.
const PromptEnrichmentSystemPrompt = `You are an expert creative advertising prompt engineer. Your job is to take a concept brief and the creative professional's identity, then produce a detailed image generation prompt for a production-quality advertisement.

Rules:
- Output ONLY the image generation prompt text — no explanations, no preamble, no quotes
- This is an ADVERTISEMENT, not just a pretty image. Include:
  - The product/brand prominently featured
  - Headline text to render on the image (in quotes, e.g. "Taste the Wild")
  - Tagline or call-to-action text
  - Layout/composition that leaves space for copy or integrates text naturally
  - Color palette that reinforces the brand message
  - Mood, lighting, and style that sell the product
  - Target audience and emotional appeal
- Specify the ad format: print ad, social media post, billboard, etc.
- Include what to AVOID (e.g., "no watermarks", "avoid cluttered backgrounds")
- Keep the prompt under 250 words — dense, specific, and actionable
- Incorporate the creative professional's aesthetic identity and style preferences
- Think like a senior art director briefing a production team`

// toolGenerateImageForAgent enriches the prompt using the agent's identity before calling Gemini.
func (s *Server) toolGenerateImageForAgent(slug, channelID string, agent *Agent, argsJSON string) string {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Prompt == "" {
		return "Error: prompt is required"
	}

	apiKey, model := s.getBrainSettings(slug)
	geminiKey := s.getGeminiAPIKey(slug)
	if geminiKey == "" {
		return "Error: no Gemini API key configured. Set it in Brain Settings."
	}

	// Use the agent's model (or cheap memory_model) for enrichment — NOT Gemini
	enrichModel := model
	_, _, memModel := s.getMemorySettings(slug)
	if memModel != "" {
		enrichModel = memModel
	}

	// Build agent context for enrichment
	var agentContext string
	if agent.Role != "" {
		agentContext += "Role: " + agent.Role + "\n"
	}
	if agent.Goal != "" {
		agentContext += "Goal: " + agent.Goal + "\n"
	}
	if agent.Backstory != "" {
		agentContext += "Background: " + agent.Backstory + "\n"
	}

	// Load agent's skills — find the image/ad-related skill for prompt formula
	agentSkills := brain.LoadAgentSkills(s.cfg.DataDir, slug, agent.ID)
	var skillPlaybook string
	for _, sk := range agentSkills {
		// Include any skill related to ad creative / image generation
		if strings.Contains(strings.ToLower(sk.Name), "ad") ||
			strings.Contains(strings.ToLower(sk.Name), "creative") ||
			strings.Contains(strings.ToLower(sk.Name), "image") ||
			strings.Contains(strings.ToLower(sk.Name), "visual") {
			skillPlaybook += "\n\n## Skill: " + sk.Name + "\n" + sk.Prompt
		}
	}

	// Enrich the prompt via LLM
	enrichedPrompt := args.Prompt
	if apiKey != "" {
		client := brain.NewClient(apiKey, enrichModel)
		userMsg := fmt.Sprintf("## Creative Professional Identity\n%s\n## Concept Brief\n%s", agentContext, args.Prompt)
		if skillPlaybook != "" {
			userMsg += "\n\n## Prompt Engineering Playbook (FOLLOW THIS STRUCTURE)" + skillPlaybook
		}
		userMsg += "\n\nCraft the image generation prompt following the structured formula above. MUST include headline text and CTA text to render on the image:"
		result, enrichUsage, err := client.Complete(PromptEnrichmentSystemPrompt, []brain.Message{
			{Role: "user", Content: userMsg},
		})
		if err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("prompt enrichment failed, using raw prompt")
		} else {
			enrichedPrompt = strings.TrimSpace(result)
			s.trackUsage(slug, enrichUsage, enrichModel, "image", channelID, "")
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("prompt", truncate(enrichedPrompt, 200)).Msg("enriched prompt")
		}
	}

	imageModel := agent.ImageModel
	if imageModel == "" {
		imageModel = s.getImageModel(slug)
	}
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("model", imageModel).Msg("using Gemini model")

	text, imageData, mimeType, err := brain.GenerateImageGemini(geminiKey, imageModel, enrichedPrompt)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("image generation error")
		return "Error generating image: " + err.Error()
	}

	if mimeType == "" {
		mimeType = "image/png"
	}
	saved := s.saveBase64ImageBlob(slug, channelID, imageData, mimeType)
	if saved == "" {
		return "Image generated but failed to save"
	}

	// Return: Gemini text + image + collapsible prompt (special marker for frontend)
	result := strings.TrimSpace(text) + saved
	result += fmt.Sprintf("\n\n<image-prompt>\n%s\n</image-prompt>", enrichedPrompt)
	return result
}

// toolGenerateImage calls the Gemini API to generate an image from a prompt.
// Returns markdown image reference or error string (used by Brain, not agents).
func (s *Server) toolGenerateImage(slug, channelID, argsJSON string) string {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Prompt == "" {
		return "Error: prompt is required"
	}

	geminiKey := s.getGeminiAPIKey(slug)
	if geminiKey == "" {
		return "Error: no Gemini API key configured. Set it in Brain Settings."
	}

	imageModel := s.getImageModel(slug)
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("model", imageModel).Msg("using Gemini model")

	text, imageData, mimeType, err := brain.GenerateImageGemini(geminiKey, imageModel, args.Prompt)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("image generation error")
		return "Error generating image: " + err.Error()
	}

	// Save as blob
	if mimeType == "" {
		mimeType = "image/png"
	}
	saved := s.saveBase64ImageBlob(slug, channelID, imageData, mimeType)
	if saved == "" {
		return "Image generated but failed to save"
	}

	result := strings.TrimSpace(text) + saved
	return result
}

// toolSendEmail sends an email via the configured outbound SMTP.
func (s *Server) toolSendEmail(slug, argsJSON string) string {
	var args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.To == "" || args.Subject == "" || args.Body == "" {
		return "Error: to, subject, and body are all required"
	}

	host := s.getBrainSetting(slug, "email_outbound_host")
	if host == "" {
		return "Error: outbound email not configured. Set SMTP settings in Brain Settings → Integrations."
	}

	s.sendOutboundEmail(slug, args.To, args.Subject, args.Body, "")
	return fmt.Sprintf("Email sent to %s with subject: %s", args.To, args.Subject)
}

// toolSendTelegram sends a Telegram message via the channel's linked chat.
func (s *Server) toolSendTelegram(slug, channelID, argsJSON string) string {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Message == "" {
		return "Error: message is required"
	}

	botToken := s.getBrainSetting(slug, "telegram_bot_token")
	if botToken == "" {
		return "Error: Telegram bot not configured. Set bot token in Brain Settings → Integrations."
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Find linked Telegram chat for this channel
	var chatIDStr string
	_ = wdb.DB.QueryRow(
		"SELECT source_key FROM channel_integrations WHERE channel_id = ? AND source_type = 'telegram'",
		channelID,
	).Scan(&chatIDStr)

	if chatIDStr == "" {
		return "Error: this channel is not linked to a Telegram chat"
	}

	var chatID int64
	fmt.Sscanf(chatIDStr, "%d", &chatID)
	if chatID == 0 {
		return "Error: invalid Telegram chat ID"
	}

	if err := sendTelegramMessage(botToken, chatID, args.Message); err != nil {
		return "Error sending Telegram message: " + err.Error()
	}

	return fmt.Sprintf("Message sent to Telegram chat %s", chatIDStr)
}

// toolWebSearch searches the web using Brave Search API (preferred) or DuckDuckGo fallback.
func (s *Server) toolWebSearch(slug, argsJSON string) string {
	log := logger.WithCategory(logger.CatBrain)
	var args struct {
		Query      string `json:"query"`
		NumResults int    `json:"num_results"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Query == "" {
		return "Error: query is required"
	}
	if args.NumResults <= 0 {
		args.NumResults = 5
	}
	if args.NumResults > 10 {
		args.NumResults = 10
	}
	log.Info().Str("query", args.Query).Int("num", args.NumResults).Msg("web_search starting")

	// Try Brave Search API first (works reliably from cloud servers)
	var braveKey string
	if wdb, err := s.ws.Open(slug); err == nil {
		_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'brave_api_key'").Scan(&braveKey)
	}
	if braveKey != "" {
		log.Info().Msg("web_search: trying Brave API")
		if result := searchBrave(braveKey, args.Query, args.NumResults); result != "" {
			return result
		}
		log.Warn().Msg("web_search: Brave API returned no results")
	}

	// Try MCP web search tools (ddg__duckduckgo_web_search, brave__search, etc.)
	mgr := s.getMCPManager(slug)
	if mgr != nil {
		for _, t := range mgr.AllTools() {
			name := strings.ToLower(t.QualName)
			// Only match actual web search tools, not memory/knowledge/workspace search
			isWebSearch := (strings.Contains(name, "web_search") || strings.Contains(name, "web-search") ||
				(strings.HasPrefix(name, "ddg__") && strings.Contains(name, "search")) ||
				(strings.HasPrefix(name, "brave__") && strings.Contains(name, "search")) ||
				(strings.HasPrefix(name, "searx") && strings.Contains(name, "search")))
			if !isWebSearch {
				continue
			}
			log.Info().Str("tool", t.QualName).Msg("web_search: trying MCP tool")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			mcpArgs := map[string]any{"query": args.Query}
			result, err := mgr.CallTool(ctx, t.QualName, mcpArgs)
			cancel()
			if err == nil && result != "" && len(result) > 50 && !strings.Contains(result, "error") {
				return fmt.Sprintf("Web search results for \"%s\":\n\n%s", args.Query, result)
			}
			if err != nil {
				log.Warn().Err(err).Str("tool", t.QualName).Msg("web_search: MCP tool failed")
			}
		}
	}

	// Try direct Google scrape (no API key needed)
	log.Info().Msg("web_search: trying Google scrape")
	if googleResult, err := brain2.SearchGoogle(args.Query, args.NumResults); err == nil && googleResult != "" {
		return googleResult
	} else if err != nil {
		log.Warn().Err(err).Msg("web_search: Google scrape failed")
	}

	// Fallback: DuckDuckGo HTML scraping
	log.Info().Msg("web_search: trying DuckDuckGo HTML")
	result := searchDDG(args.Query, args.NumResults)
	if strings.Contains(result, "No results found") {
		log.Warn().Str("result", result).Msg("web_search: DDG returned no results")
		// Try DuckDuckGo Lite as last resort
		log.Info().Msg("web_search: trying DuckDuckGo Lite")
		liteResult := searchDDGLite(args.Query, args.NumResults)
		if !strings.Contains(liteResult, "No results found") {
			return liteResult
		}
		log.Warn().Msg("web_search: all providers failed")
	}
	return result
}

func (s *Server) toolSearchX(slug, argsJSON string) string {
	log := logger.WithCategory(logger.CatBrain)
	var args struct {
		Query    string   `json:"query"`
		FromDate string   `json:"from_date"`
		ToDate   string   `json:"to_date"`
		XHandles []string `json:"x_handles"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Query == "" {
		return "Error: query is required"
	}

	xaiKey := s.getXAIKey(slug)
	if xaiKey == "" {
		return "Error: xAI API key is not configured. Add it in Brain settings."
	}

	// x_search requires grok-4 family — override workspace model
	model := "grok-4"

	tool := brain.XSearchTool{
		Type:            "x_search",
		AllowedXHandles: args.XHandles,
		FromDate:        args.FromDate,
		ToDate:          args.ToDate,
	}

	client := brain.NewXAIClient(xaiKey, model)
	log.Info().Str("query", args.Query).Str("model", model).Msg("search_x starting")

	text, citations, err := client.CompleteXSearch(args.Query, tool)
	if err != nil {
		log.Error().Err(err).Msg("search_x failed")
		return "Error searching X: " + err.Error()
	}

	var sb strings.Builder
	sb.WriteString(text)
	if len(citations) > 0 {
		sb.WriteString("\n\n---\n**Sources:**\n")
		for i, url := range citations {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, url)
		}
	}
	return sb.String()
}

// searchBrave uses the Brave Search API. Free tier: 2000 queries/month.
func searchBrave(apiKey, query string, numResults int) string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.search.brave.com/res/v1/web/search", nil)
	q := req.URL.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", numResults))
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", apiKey)

	log := logger.WithCategory(logger.CatBrain)
	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("Brave API request failed")
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Warn().Int("status", resp.StatusCode).Msg("Brave API non-200 response")
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return ""
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				URL         string `json:"url"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Web.Results) == 0 {
		return ""
	}

	var lines []string
	for i, r := range result.Web.Results {
		if i >= numResults {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. **%s**\n   %s\n   %s", i+1, r.Title, r.Description, r.URL))
	}
	return fmt.Sprintf("Search results for \"%s\":\n\n%s", query, strings.Join(lines, "\n\n"))
}

// searchDDG scrapes DuckDuckGo HTML results (may be blocked from cloud IPs).
func searchDDG(query string, numResults int) string {
	client := &http.Client{Timeout: 10 * time.Second}
	formData := url.Values{"q": {query}}
	req, _ := http.NewRequest("POST", "https://html.duckduckgo.com/html/", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://duckduckgo.com/")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}

	type searchResult struct {
		Title, Snippet, URL string
	}
	var results []searchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			for _, a := range n.Attr {
				if a.Key == "class" && strings.Contains(a.Val, "result__body") {
					r := extractDDGResult(n)
					if r.Title != "" && r.URL != "" {
						results = append(results, searchResult(r))
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(results) == 0 {
		return fmt.Sprintf("No results found for \"%s\" (search provider may be unavailable from this server)", query)
	}
	if len(results) > numResults {
		results = results[:numResults]
	}

	var lines []string
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. **%s**\n   %s\n   %s", i+1, r.Title, r.Snippet, r.URL))
	}
	return fmt.Sprintf("Search results for \"%s\":\n\n%s", query, strings.Join(lines, "\n\n"))
}

// searchDDGLite uses DuckDuckGo Lite (lighter page, less likely to be blocked).
func searchDDGLite(query string, numResults int) string {
	client := &http.Client{Timeout: 10 * time.Second}
	formData := url.Values{"q": {query}}
	req, _ := http.NewRequest("POST", "https://lite.duckduckgo.com/lite/", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return fmt.Sprintf("No results found for \"%s\"", query)
	}

	// DDG Lite uses a table layout with result links and snippets
	type searchResult struct {
		Title, Snippet, URL string
	}
	var results []searchResult
	var currentResult searchResult

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "class" && a.Val == "result-link" {
					currentResult.Title = textContent(n)
					for _, attr := range n.Attr {
						if attr.Key == "href" {
							currentResult.URL = attr.Val
						}
					}
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "td" {
			for _, a := range n.Attr {
				if a.Key == "class" && strings.Contains(a.Val, "result-snippet") {
					currentResult.Snippet = textContent(n)
					if currentResult.Title != "" && currentResult.URL != "" {
						results = append(results, currentResult)
						currentResult = searchResult{}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(results) == 0 {
		return fmt.Sprintf("No results found for \"%s\" (search provider may be unavailable from this server)", query)
	}
	if len(results) > numResults {
		results = results[:numResults]
	}

	var lines []string
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. **%s**\n   %s\n   %s", i+1, r.Title, r.Snippet, r.URL))
	}
	return fmt.Sprintf("Search results for \"%s\":\n\n%s", query, strings.Join(lines, "\n\n"))
}

// extractDDGResult extracts title, URL, and snippet from a DuckDuckGo result__body div.
func extractDDGResult(n *html.Node) struct {
	Title, Snippet, URL string
} {
	var result struct{ Title, Snippet, URL string }

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "a" {
				for _, a := range node.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "result__a") {
						result.Title = textContent(node)
						for _, attr := range node.Attr {
							if attr.Key == "href" {
								u := attr.Val
								// DDG wraps URLs in a redirect; extract the actual URL
								if idx := strings.Index(u, "uddg="); idx >= 0 {
									u = u[idx+5:]
									if end := strings.Index(u, "&"); end >= 0 {
										u = u[:end]
									}
									if decoded, err := url.QueryUnescape(u); err == nil {
										u = decoded
									}
								}
								result.URL = u
							}
						}
					}
				}
			}
			if node.Data == "a" || node.Data == "span" || node.Data == "div" {
				for _, a := range node.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "result__snippet") {
						result.Snippet = textContent(node)
					}
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return result
}

// textContent returns the text content of an HTML node and its descendants.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return strings.TrimSpace(sb.String())
}

// toolFetchURL fetches a URL and extracts text content.
func toolFetchURL(argsJSON string) string {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.URL == "" {
		return "Error: url is required"
	}

	parsed, err := url.Parse(args.URL)
	if err != nil {
		return "Error: invalid URL"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "Error: only http and https URLs are supported"
	}

	// SSRF protection: resolve DNS and block private IPs
	host := parsed.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return "Error: could not resolve host " + host
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return "Error: access to private/internal addresses is not allowed"
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", args.URL, nil)
	if err != nil {
		return "Error creating request: " + err.Error()
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "Error fetching URL: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Sprintf("Error: server returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "Error reading response: " + err.Error()
	}

	title, content := extractHTMLText(string(bodyBytes))

	// If standard extraction got very little, try brain2's readability extraction
	if len(content) < 200 {
		if better, err := brain2.FetchAndExtract(args.URL, 10000); err == nil && len(better) > len(content) {
			return better
		}
	}

	// Truncate to 10000 chars
	if len(content) > 10000 {
		content = content[:10000] + "\n\n[content truncated]"
	}

	if title != "" {
		return fmt.Sprintf("**Title:** %s\n\n%s", title, content)
	}
	return content
}

// isPrivateIP checks if an IP is in a private/reserved range.
func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{parseCIDR("127.0.0.0/8")},
		{parseCIDR("10.0.0.0/8")},
		{parseCIDR("172.16.0.0/12")},
		{parseCIDR("192.168.0.0/16")},
		{parseCIDR("169.254.0.0/16")},
		{parseCIDR("::1/128")},
		{parseCIDR("fc00::/7")},
		{parseCIDR("fe80::/10")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}

// toolTraceKnowledge searches memories and knowledge for provenance of a claim.
func (s *Server) toolTraceKnowledge(slug, argsJSON string) string {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}
	if args.Query == "" {
		return "Error: query is required"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	var lines []string

	// Search memories
	memories, err := brain.SearchMemoriesTyped(wdb.DB, args.Query, "", 5)
	if err == nil && len(memories) > 0 {
		for _, m := range memories {
			content := m.Content
			if len(content) > 200 {
				content = content[:197] + "..."
			}
			meta := fmt.Sprintf("**Memory (%s", m.Type)
			if m.Confidence > 0 {
				meta += fmt.Sprintf(", confidence %.2f", m.Confidence)
			}
			meta += fmt.Sprintf(`):** "%s"`, content)

			var attribution []string
			if m.Participants != "" {
				attribution = append(attribution, m.Participants)
			}
			if m.SourceChannel != "" {
				// Resolve channel name
				var cname string
				if wdb.DB.QueryRow("SELECT name FROM channels WHERE id = ?", m.SourceChannel).Scan(&cname) == nil {
					attribution = append(attribution, "in #"+cname)
				}
			}
			if t, pErr := time.Parse(time.RFC3339, m.CreatedAt); pErr == nil {
				attribution = append(attribution, t.Format("Jan 2"))
			}
			if m.Source != "" {
				attribution = append(attribution, "extracted from "+m.Source)
			}
			if len(attribution) > 0 {
				meta += "\n— " + strings.Join(attribution, ", ")
			}
			lines = append(lines, meta)
		}
	}

	// Search knowledge
	apiKey, _ := s.getBrainSettings(slug)
	knowledgeResult := brain.SearchKnowledgeForTool(wdb.DB, args.Query, brain.SemanticOpts{
		VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
	})
	if !strings.HasPrefix(knowledgeResult, "No knowledge") {
		lines = append(lines, "\n"+knowledgeResult)
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No sources found for: %s", args.Query)
	}

	return fmt.Sprintf("Found %d sources:\n\n%s", len(lines), strings.Join(lines, "\n\n"))
}

// handleExecuteTool executes a single tool call from a client-side LLM.
// POST /api/workspaces/{slug}/brain/execute-tool
func (s *Server) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var req struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	call := brain.ToolCall{
		ID:   "webllm_" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		Type: "function",
	}
	call.Function.Name = req.Name
	call.Function.Arguments = req.Arguments

	channelID := req.ChannelID
	result := s.executeTool(slug, channelID, "", call)

	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}

func (s *Server) handleGetBrainTools(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	tools := s.getAllTools(slug)
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}
