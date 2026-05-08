// Package brain2 implements the Brain v2 pipeline: Plan → Execute → Synthesize → Reflect.
// It reuses v1's tools, context assembly, and memory system — no v1 code is modified.
package brain2

import (
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
)

// Metrics captures timing and cost data for a single Brain v2 invocation.
type Metrics struct {
	Version      string        `json:"version"`
	TotalLatency time.Duration `json:"total_latency"`
	PlanLatency  time.Duration `json:"plan_latency"`
	ExecLatency  time.Duration `json:"exec_latency"`
	SynthLatency time.Duration `json:"synth_latency"`
	LLMCalls     int           `json:"llm_calls"`
	ToolCalls    int           `json:"tool_calls"`
	ToolsParallel int          `json:"tools_parallel"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CostUSD      float64       `json:"cost_usd"`
	Model        string        `json:"model"`
	PlannerModel string        `json:"planner_model"`
	Success      bool          `json:"success"`
}

// Step represents a single planned action in the execution pipeline.
type Step struct {
	ID        string   `json:"id"`
	Tool      string   `json:"tool"`
	Args      string   `json:"args"`      // JSON string of arguments
	DependsOn []string `json:"depends_on"` // step IDs that must complete first
}

// Plan is the output of the Planner stage.
type Plan struct {
	Steps        []Step   `json:"steps"`
	DirectAnswer bool     `json:"direct_answer"` // true = skip executor, no tools needed
	ScopedTools  []string `json:"tools"`          // only these tools sent to executor
}

// StepResult holds the output of a single executed step.
type StepResult struct {
	StepID  string `json:"step_id"`
	Tool    string `json:"tool"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
	Elapsed time.Duration `json:"elapsed"`
}

// ToolExecutor is the function signature for executing a single tool call.
// This matches the existing server.executeTool signature so we can reuse it.
type ToolExecutor func(slug, channelID, senderMemberID string, call brain.ToolCall) string

// LLMClient abstracts the LLM provider for the pipeline stages.
// This matches the existing brainCompleter interface in server/brain.go.
type LLMClient interface {
	Complete(systemPrompt string, messages []brain.Message) (string, *brain.CompletionUsage, error)
	CompleteWithTools(systemPrompt string, messages []brain.Message, tools []brain.ToolDef) (string, []brain.ToolCall, *brain.CompletionUsage, error)
}

// PipelineConfig holds the configuration for a Brain v2 pipeline run.
type PipelineConfig struct {
	Slug          string
	ChannelID     string
	ParentID      string
	SenderName    string
	Content       string
	SystemPrompt  string
	Messages      []brain.Message
	AllTools      []brain.ToolDef
	Client        LLMClient // main model (synthesizer)
	PlannerClient LLMClient // fast model (planner) — nil means use Client
	MaxDepth      int       // max tool-calling iterations (default 5)
	// MaxCostUSD aborts the tool loop once cumulative LLM spend on this
	// turn (sum of every CompleteWithTools / Complete usage.cost) crosses
	// this threshold. Zero (the default) disables the cap. Inspired by the
	// OpenRouter Agent SDK's `maxCost` stop condition — vendor-neutral
	// because we read .cost off the standard CompletionUsage already.
	MaxCostUSD  float64
	ExecuteTool ToolExecutor
}

// PipelineResult holds the output of a complete pipeline run.
type PipelineResult struct {
	Response  string
	Metrics   Metrics
	ToolsUsed []string
	// LastError carries the underlying LLM-client error (e.g. an OpenRouter
	// auth failure or upstream 5xx) when the pipeline produced an empty
	// response. The server handler turns this into a chat-friendly message
	// via FriendlyError so users see "your key isn't working" instead of a
	// silent "I processed your request but couldn't generate a response."
	LastError string
	// CostUSD is the cumulative LLM spend across every round of this turn,
	// including completions, follow-up rounds, and the synthesizer. Used
	// by the MaxCostUSD stop condition and surfaced to the observatory.
	CostUSD float64
	// Items is the typed-event stream for this turn — a sequence of
	// {text, reasoning, tool_call, tool_result} entries the UI can render
	// distinctly (collapsible reasoning blocks, tool-call chips, etc).
	// Same shape works for both engines (v3's `thinking` blocks map to
	// `reasoning` cleanly). Populated even without active streaming.
	Items []brain.StreamItem
}

// Run executes the Brain v2 pipeline with self-correcting tool loop.
// This is the main entry point called from server/brain2.go.
func Run(cfg PipelineConfig) PipelineResult {
	start := time.Now()
	m := Metrics{Version: "v2", Model: "unknown", Success: false}

	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 5
	}

	// Self-correcting tool loop: LLM decides tools, validates, retries on error.
	// This is the core v2 improvement over v1's fixed 2-round loop.
	execStart := time.Now()
	plan := Plan{ScopedTools: nil} // no scoping — model sees all tools
	exec := RunExecutor(cfg, plan)
	m.ExecLatency = time.Since(execStart)
	m.ToolCalls = len(exec.Results)
	costUSD := exec.CostUSD
	items := exec.Items

	// Extract response
	var response string
	synthStart := time.Now()

	// Check if executor produced a direct text response (no tools called)
	for _, r := range exec.Results {
		if r.Tool == "_response" && r.Result != "" {
			response = r.Result
			break
		}
	}

	// If tools were called, synthesize a final response from the results.
	// Skip when the cost cap broke us out — the budget-break message in
	// exec.Results is already the right reply, no point burning more spend.
	if response == "" && len(exec.ToolsUsed) > 0 && !exec.BudgetExceeded {
		response = RunSynthesizer(cfg, plan, exec.Results)
		m.LLMCalls++
		if response != "" {
			items = append(items, brain.StreamItem{Kind: brain.ItemKindText, Text: response})
		}
	}

	lastErr := exec.LastErr

	// Last resort: if still empty, do a plain completion
	if response == "" {
		fmt.Printf("[brain2] pipeline: no response from executor, trying plain Complete\n")
		plainResp, plainUsage, err := cfg.Client.Complete(cfg.SystemPrompt, cfg.Messages)
		if plainUsage != nil {
			costUSD += plainUsage.Cost
		}
		fmt.Printf("[brain2] pipeline fallback: err=%v len=%d\n", err, len(plainResp))
		if err == nil && plainResp != "" {
			response = plainResp
			items = append(items, brain.StreamItem{Kind: brain.ItemKindText, Text: plainResp})
		} else if err != nil {
			lastErr = err.Error()
		}
		m.LLMCalls++
	}

	m.SynthLatency = time.Since(synthStart)

	m.Success = response != ""
	m.TotalLatency = time.Since(start)
	return PipelineResult{
		Response:  response,
		Metrics:   m,
		ToolsUsed: exec.ToolsUsed,
		LastError: lastErr,
		CostUSD:   costUSD,
		Items:     items,
	}
}

// FriendlyError converts a captured LLM-client error string from a v2 turn
// into a chat-visible message that points at the actual fix (or back at the
// engine selector). Matches brain3.errorResponse semantics so v2 and v3 send
// the same shape of message for the same admin-actionable failure modes.
//
// Returns an empty string if no friendly mapping applies — caller should
// keep its generic fallback in that case.
func FriendlyError(msg string) string {
	if msg == "" {
		return ""
	}
	switch {
	case strings.Contains(msg, "User not found"),
		strings.Contains(strings.ToLower(msg), "invalid api key"),
		strings.Contains(msg, "401"),
		strings.Contains(msg, "Authentication failed"):
		return "Brain's OpenRouter API key isn't working — OpenRouter rejected the request. An admin can re-enter the key in **Settings → Brain → API Keys**, or switch the engine to Claude in **Settings → Brain → Engine**."
	case strings.Contains(msg, "402"),
		strings.Contains(strings.ToLower(msg), "insufficient credits"),
		strings.Contains(strings.ToLower(msg), "credit"):
		return "OpenRouter account is out of credits. An admin can top up at openrouter.ai/credits, or switch the engine to Claude in **Settings → Brain → Engine**."
	case strings.Contains(msg, "429"), strings.Contains(strings.ToLower(msg), "rate limit"):
		return "OpenRouter is rate-limiting this workspace. Try again in a minute, or switch the engine to Claude in **Settings → Brain → Engine**."
	case strings.Contains(msg, "model_not_found"),
		strings.Contains(strings.ToLower(msg), "no allowed providers"):
		return "The selected OpenRouter model isn't available — pick a different one in **Settings → Brain → Models**."
	}
	return ""
}
