package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/nexus-chat/nexus/internal/auth"
)

type traceRow struct {
	ID              string  `json:"id"`
	ActionLogID     string  `json:"action_log_id"`
	BrainVersion    string  `json:"brain_version"`
	ChannelID       string  `json:"channel_id"`
	SenderName      string  `json:"sender_name"`
	TriggerText     string  `json:"trigger_text"`
	Model           string  `json:"model"`
	TotalLatencyMs  int64   `json:"total_latency_ms"`
	ExecLatencyMs   int64   `json:"exec_latency_ms"`
	SynthLatencyMs  int64   `json:"synth_latency_ms"`
	ToolCalls       int     `json:"tool_calls"`
	LLMCalls        int     `json:"llm_calls"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	SkillsMatched   []string `json:"skills_matched"`
	MemoriesIncluded int    `json:"memories_included"`
	KnowledgeChunks  int    `json:"knowledge_chunks"`
	Success         bool    `json:"success"`
	ErrorMessage    string  `json:"error_message"`
	CreatedAt       string  `json:"created_at"`
}

type traceStepRow struct {
	ID            int    `json:"id"`
	StepOrder     int    `json:"step_order"`
	StepType      string `json:"step_type"`
	ToolName      string `json:"tool_name"`
	ArgsSummary   string `json:"args_summary"`
	ResultSummary string `json:"result_summary"`
	Error         string `json:"error"`
	LatencyMs     int64  `json:"latency_ms"`
	CreatedAt     string `json:"created_at"`
}

// handleListTraces returns recent brain traces (admin only).
func (s *Server) handleListTraces(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	rows, err := wdb.DB.Query(`SELECT id, action_log_id, brain_version, channel_id, sender_name,
		trigger_text, model, total_latency_ms, exec_latency_ms, synth_latency_ms,
		tool_calls, llm_calls, input_tokens, output_tokens, cost_usd,
		skills_matched, memories_included, knowledge_chunks, success, error_message, created_at
		FROM brain_traces ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	traces := []traceRow{}
	for rows.Next() {
		var t traceRow
		var skillsJSON string
		if err := rows.Scan(&t.ID, &t.ActionLogID, &t.BrainVersion, &t.ChannelID, &t.SenderName,
			&t.TriggerText, &t.Model, &t.TotalLatencyMs, &t.ExecLatencyMs, &t.SynthLatencyMs,
			&t.ToolCalls, &t.LLMCalls, &t.InputTokens, &t.OutputTokens, &t.CostUSD,
			&skillsJSON, &t.MemoriesIncluded, &t.KnowledgeChunks, &t.Success, &t.ErrorMessage, &t.CreatedAt,
		); err != nil {
			continue
		}
		json.Unmarshal([]byte(skillsJSON), &t.SkillsMatched)
		if t.SkillsMatched == nil {
			t.SkillsMatched = []string{}
		}
		traces = append(traces, t)
	}

	var total int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM brain_traces").Scan(&total)

	writeJSON(w, http.StatusOK, map[string]any{
		"traces": traces,
		"total":  total,
	})
}

// handleGetTrace returns a single trace with its steps (admin only).
func (s *Server) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	traceID := r.PathValue("traceID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var t traceRow
	var skillsJSON string
	err = wdb.DB.QueryRow(`SELECT id, action_log_id, brain_version, channel_id, sender_name,
		trigger_text, model, total_latency_ms, exec_latency_ms, synth_latency_ms,
		tool_calls, llm_calls, input_tokens, output_tokens, cost_usd,
		skills_matched, memories_included, knowledge_chunks, success, error_message, created_at
		FROM brain_traces WHERE id = ?`, traceID).Scan(
		&t.ID, &t.ActionLogID, &t.BrainVersion, &t.ChannelID, &t.SenderName,
		&t.TriggerText, &t.Model, &t.TotalLatencyMs, &t.ExecLatencyMs, &t.SynthLatencyMs,
		&t.ToolCalls, &t.LLMCalls, &t.InputTokens, &t.OutputTokens, &t.CostUSD,
		&skillsJSON, &t.MemoriesIncluded, &t.KnowledgeChunks, &t.Success, &t.ErrorMessage, &t.CreatedAt,
	)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	json.Unmarshal([]byte(skillsJSON), &t.SkillsMatched)
	if t.SkillsMatched == nil {
		t.SkillsMatched = []string{}
	}

	// Fetch steps
	stepRows, err := wdb.DB.Query(`SELECT id, step_order, step_type, tool_name, args_summary, result_summary, error, latency_ms, created_at
		FROM brain_trace_steps WHERE trace_id = ? ORDER BY step_order`, traceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "steps query failed")
		return
	}
	defer stepRows.Close()

	steps := []traceStepRow{}
	for stepRows.Next() {
		var s traceStepRow
		if err := stepRows.Scan(&s.ID, &s.StepOrder, &s.StepType, &s.ToolName, &s.ArgsSummary, &s.ResultSummary, &s.Error, &s.LatencyMs, &s.CreatedAt); err != nil {
			continue
		}
		steps = append(steps, s)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"trace": t,
		"steps": steps,
	})
}
