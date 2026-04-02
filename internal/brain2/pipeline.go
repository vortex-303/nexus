// Package brain2 implements the Brain v2 pipeline: Plan → Execute → Synthesize → Reflect.
// It reuses v1's tools, context assembly, and memory system — no v1 code is modified.
package brain2

import (
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
	ExecuteTool   ToolExecutor
}

// PipelineResult holds the output of a complete pipeline run.
type PipelineResult struct {
	Response string
	Metrics  Metrics
	ToolsUsed []string
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
	results, toolsUsed := RunExecutor(cfg, plan)
	m.ExecLatency = time.Since(execStart)
	m.ToolCalls = len(results)

	// Extract response
	var response string
	synthStart := time.Now()

	// Check if executor produced a direct text response (no tools called)
	for _, r := range results {
		if r.Tool == "_response" && r.Result != "" {
			response = r.Result
			break
		}
	}

	// If tools were called, synthesize a final response from the results
	if response == "" && len(toolsUsed) > 0 {
		response = RunSynthesizer(cfg, plan, results)
		m.LLMCalls++
	}

	// Last resort: if still empty, do a plain completion
	if response == "" {
		plainResp, _, err := cfg.Client.Complete(cfg.SystemPrompt, cfg.Messages)
		if err == nil && plainResp != "" {
			response = plainResp
		}
		m.LLMCalls++
	}

	m.SynthLatency = time.Since(synthStart)

	m.Success = response != ""
	m.TotalLatency = time.Since(start)
	return PipelineResult{Response: response, Metrics: m, ToolsUsed: toolsUsed}
}
