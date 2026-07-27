package brain2

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TraceStep records a single step in a Brain pipeline execution.
type TraceStep struct {
	StepType      string // "tool_call", "llm_call", "validation_error", "context_assembly"
	ToolName      string
	ArgsSummary   string
	ResultSummary string
	Error         string
	LatencyMs     int64
}

// ContextStats captures what was injected into the system prompt.
type ContextStats struct {
	SkillsMatched   []string
	MemoriesIncluded int
	KnowledgeChunks  int
}

// TraceCollector accumulates trace steps during a pipeline run.
// It is goroutine-safe and optional (nil collector is a no-op).
type TraceCollector struct {
	mu    sync.Mutex
	Steps []TraceStep
	Stats ContextStats
}

// NewTraceCollector creates a new collector.
func NewTraceCollector() *TraceCollector {
	return &TraceCollector{}
}

// AddStep appends a step to the trace.
func (tc *TraceCollector) AddStep(s TraceStep) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	// Truncate summaries to keep DB rows reasonable
	if len(s.ArgsSummary) > 200 {
		s.ArgsSummary = s.ArgsSummary[:200]
	}
	if len(s.ResultSummary) > 500 {
		s.ResultSummary = s.ResultSummary[:500]
	}
	tc.Steps = append(tc.Steps, s)
}

// AddToolCall is a convenience method for recording a tool execution.
func (tc *TraceCollector) AddToolCall(toolName, args, result, errMsg string, elapsed time.Duration) {
	tc.AddStep(TraceStep{
		StepType:      "tool_call",
		ToolName:      toolName,
		ArgsSummary:   args,
		ResultSummary: result,
		Error:         errMsg,
		LatencyMs:     elapsed.Milliseconds(),
	})
}

// AddLLMCall records an LLM invocation.
func (tc *TraceCollector) AddLLMCall(model string, elapsed time.Duration, errMsg string) {
	tc.AddStep(TraceStep{
		StepType:  "llm_call",
		ToolName:  model,
		LatencyMs: elapsed.Milliseconds(),
		Error:     errMsg,
	})
}

// AddValidationError records a tool validation failure.
func (tc *TraceCollector) AddValidationError(toolName, errMsg string) {
	tc.AddStep(TraceStep{
		StepType: "validation_error",
		ToolName: toolName,
		Error:    errMsg,
	})
}

// SetContextStats records context assembly metrics.
func (tc *TraceCollector) SetContextStats(stats ContextStats) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.Stats = stats
}

// TraceRecord is the top-level trace to be persisted.
type TraceRecord struct {
	ID              string
	ActionLogID     string
	BrainVersion    string
	ChannelID       string
	SenderName      string
	TriggerText     string
	Model           string
	TotalLatencyMs  int64
	ExecLatencyMs   int64
	SynthLatencyMs  int64
	ToolCalls       int
	LLMCalls        int
	InputTokens     int
	OutputTokens    int
	CostUSD         float64
	SkillsMatched   []string
	MemoriesIncluded int
	KnowledgeChunks  int
	Success         bool
	ErrorMessage    string
}

// FlushToDB persists the trace and its steps to the database.
func (tc *TraceCollector) FlushToDB(db *sql.DB, rec TraceRecord) error {
	if tc == nil || db == nil {
		return nil
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()

	// Fill in context stats from collector
	if len(rec.SkillsMatched) == 0 {
		rec.SkillsMatched = tc.Stats.SkillsMatched
	}
	if rec.MemoriesIncluded == 0 {
		rec.MemoriesIncluded = tc.Stats.MemoriesIncluded
	}
	if rec.KnowledgeChunks == 0 {
		rec.KnowledgeChunks = tc.Stats.KnowledgeChunks
	}

	skillsJSON, _ := json.Marshal(rec.SkillsMatched)
	if skillsJSON == nil {
		skillsJSON = []byte("[]")
	}

	// Truncate trigger text
	triggerText := rec.TriggerText
	if len(triggerText) > 200 {
		triggerText = triggerText[:200]
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin trace flush: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO brain_traces (id, action_log_id, brain_version, channel_id, sender_name,
		trigger_text, model, total_latency_ms, exec_latency_ms, synth_latency_ms,
		tool_calls, llm_calls, input_tokens, output_tokens, cost_usd,
		skills_matched, memories_included, knowledge_chunks, success, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.ActionLogID, rec.BrainVersion, rec.ChannelID, rec.SenderName,
		triggerText, rec.Model, rec.TotalLatencyMs, rec.ExecLatencyMs, rec.SynthLatencyMs,
		rec.ToolCalls, rec.LLMCalls, rec.InputTokens, rec.OutputTokens, rec.CostUSD,
		string(skillsJSON), rec.MemoriesIncluded, rec.KnowledgeChunks, rec.Success, rec.ErrorMessage,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("insert brain_traces: %w", err)
	}

	for i, step := range tc.Steps {
		_, err = tx.Exec(`INSERT INTO brain_trace_steps (trace_id, step_order, step_type, tool_name, args_summary, result_summary, error, latency_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.ID, i, step.StepType, step.ToolName, step.ArgsSummary, step.ResultSummary, step.Error, step.LatencyMs,
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("insert brain_trace_steps: %w", err)
		}
	}

	return tx.Commit()
}
