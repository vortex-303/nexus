package brain3

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TraceStep records a single step in a Brain v3 pipeline execution.
type TraceStep struct {
	StepType      string // "tool_call" | "llm_call"
	ToolName      string
	ArgsSummary   string
	ResultSummary string
	Error         string
	LatencyMs     int64
}

// TraceCollector accumulates trace steps during a pipeline run. Goroutine-safe.
// Implements brain3.TraceRecorder; callers can pass it directly into
// PipelineConfig.Trace.
type TraceCollector struct {
	mu    sync.Mutex
	Steps []TraceStep
}

// NewTraceCollector creates a new collector.
func NewTraceCollector() *TraceCollector {
	return &TraceCollector{}
}

// AddToolCall records a tool execution. Implements TraceRecorder.
func (tc *TraceCollector) AddToolCall(toolName, args, result, errMsg string, elapsed time.Duration) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if len(args) > 200 {
		args = args[:200]
	}
	if len(result) > 500 {
		result = result[:500]
	}
	tc.Steps = append(tc.Steps, TraceStep{
		StepType:      "tool_call",
		ToolName:      toolName,
		ArgsSummary:   args,
		ResultSummary: result,
		Error:         errMsg,
		LatencyMs:     elapsed.Milliseconds(),
	})
}

// AddLLMCall records an LLM invocation. Implements TraceRecorder.
func (tc *TraceCollector) AddLLMCall(model string, elapsed time.Duration, errMsg string) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.Steps = append(tc.Steps, TraceStep{
		StepType:  "llm_call",
		ToolName:  model,
		LatencyMs: elapsed.Milliseconds(),
		Error:     errMsg,
	})
}

// TraceRecord is the top-level trace persisted into brain_traces. Mirrors
// brain2's TraceRecord shape so the existing observatory UI renders v3
// alongside v2 with no UI changes (brain_version='v3' discriminates).
type TraceRecord struct {
	ID               string
	ActionLogID      string
	BrainVersion     string
	ChannelID        string
	SenderName       string
	TriggerText      string
	Model            string
	TotalLatencyMs   int64
	ExecLatencyMs    int64
	SynthLatencyMs   int64
	ToolCalls        int
	LLMCalls         int
	InputTokens      int
	OutputTokens     int
	CostUSD          float64
	SkillsMatched    []string
	MemoriesIncluded int
	KnowledgeChunks  int
	Success          bool
	ErrorMessage     string
}

// FlushToDB persists the trace and its steps to brain_traces /
// brain_trace_steps. Best-effort: returns the first error encountered.
func (tc *TraceCollector) FlushToDB(db *sql.DB, rec TraceRecord) error {
	if tc == nil || db == nil {
		return nil
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()

	skillsJSON, _ := json.Marshal(rec.SkillsMatched)
	if skillsJSON == nil {
		skillsJSON = []byte("[]")
	}
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
