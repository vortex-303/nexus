package brain3

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
)

// Metrics captures timing data for a single Brain v3 invocation, mirroring
// brain2.Metrics so the observatory UI can render v2/v3 traces identically.
type Metrics struct {
	Version       string        `json:"version"`
	TotalLatency  time.Duration `json:"total_latency"`
	ProvisionMs   int64         `json:"provision_ms"`
	SessionMs     int64         `json:"session_ms"`
	PreloadMs     int64         `json:"preload_ms"`
	StreamMs      int64         `json:"stream_ms"`
	LLMCalls      int           `json:"llm_calls"`
	ToolCalls     int           `json:"tool_calls"`
	InputTokens   int           `json:"input_tokens"`
	OutputTokens  int           `json:"output_tokens"`
	CostUSD       float64       `json:"cost_usd"`
	Model         string        `json:"model"`
	Success       bool          `json:"success"`
	AgentID       string        `json:"agent_id"`
	SessionID     string        `json:"session_id"`
	MemoryStoreID string        `json:"memory_store_id"`
}

// TraceRecorder is the minimal observability surface brain3 needs to feed
// pipeline events into the existing brain_traces / brain_trace_steps tables.
// Method shapes match brain2.TraceCollector exactly so the server handler
// can pass a *brain2.TraceCollector via duck typing — keeping brain3's
// package boundary clean (no internal/brain2 import inside this package)
// while still sharing the observatory UI between v2 and v3.
type TraceRecorder interface {
	AddToolCall(toolName, args, result, errMsg string, elapsed time.Duration)
	AddLLMCall(model string, elapsed time.Duration, errMsg string)
}

// noopTrace is used when no recorder is provided so call sites don't need
// nil checks.
type noopTrace struct{}

func (noopTrace) AddToolCall(string, string, string, string, time.Duration) {}
func (noopTrace) AddLLMCall(string, time.Duration, string)                  {}

// PipelineConfig is the input to a Brain v3 turn.
type PipelineConfig struct {
	Slug         string
	ChannelID    string
	ParentID     string // "" for channel root, message ID for thread replies
	SenderName   string
	Content      string
	SystemPrompt string             // assembled by caller; captured at agent-create time only
	Messages     []brain.Message    // unused in v3 — sessions persist their own history
	AllTools     []brain.ToolDef    // Nexus tool catalog, bridged at agent-create time
	Settings     SettingsStore
	DB           *sql.DB            // workspace DB for brain_managed_sessions
	ExecuteTool  func(slug, channelID, senderMemberID string, call brain.ToolCall) string
	Trace        TraceRecorder      // optional; pipeline substitutes a noop if nil
}

// Result is the output of a Brain v3 turn.
type Result struct {
	Response  string
	Metrics   Metrics
	ToolsUsed []string
}

// Run is the main entry point for the v3 pipeline. End-to-end flow:
//   1. Provision (or reuse) the workspace's environment + agent + memory_store.
//   2. Look up (or create) the (channel, parent) session.
//   3. Pre-load pinned.md + sender profile from the memory_store.
//   4. Build the user message with the <context> block prepended.
//   5. Open SSE stream, send the message, consume events until idle.
//   6. Return the final assistant text + metrics.
//
// On any provisioning/session error, returns a degraded Result with an
// error-shaped Response so the WS handler still posts something visible.
func Run(ctx context.Context, cfg PipelineConfig) Result {
	start := time.Now()
	m := Metrics{Version: VersionTag, Model: DefaultModel}

	if cfg.Trace == nil {
		cfg.Trace = noopTrace{}
	}

	apiKey := cfg.Settings.Get(cfg.Slug, "anthropic_api_key")
	client, err := NewClient(apiKey)
	if err != nil {
		m.TotalLatency = time.Since(start)
		return Result{Response: errorResponse(err), Metrics: m}
	}

	provStart := time.Now()
	info, err := EnsureProvisioned(ctx, cfg.Settings, cfg.Slug, cfg.SystemPrompt, cfg.AllTools)
	m.ProvisionMs = time.Since(provStart).Milliseconds()
	if err != nil {
		m.TotalLatency = time.Since(start)
		return Result{Response: errorResponse(err), Metrics: m}
	}
	m.AgentID = info.AgentID
	m.MemoryStoreID = info.MemoryStoreID

	if cfg.DB == nil {
		m.TotalLatency = time.Since(start)
		return Result{Response: "Brain v3: workspace DB is unavailable.", Metrics: m}
	}

	sessStart := time.Now()
	sessionID, err := EnsureSession(ctx, client, cfg.DB, info, cfg.ChannelID, cfg.ParentID)
	m.SessionMs = time.Since(sessStart).Milliseconds()
	if err != nil {
		m.TotalLatency = time.Since(start)
		return Result{Response: errorResponse(err), Metrics: m}
	}
	m.SessionID = sessionID

	preStart := time.Now()
	preload, err := LoadPreloadedContext(ctx, client, info.MemoryStoreID, SenderSlug(cfg.SenderName))
	m.PreloadMs = time.Since(preStart).Milliseconds()
	if err != nil {
		// Non-fatal — worst case the agent reads memory itself via its file tools.
		preload = PreloadedContext{}
	}

	userMessage := buildUserMessage(preload, cfg.SenderName, cfg.Content)

	streamStart := time.Now()
	turn, err := runTurn(ctx, client, sessionID, userMessage, cfg)
	m.StreamMs = time.Since(streamStart).Milliseconds()

	if turn.Terminated {
		// Mark the local mapping stale so the next turn provisions a fresh session.
		_ = MarkTerminated(cfg.DB, cfg.ChannelID, cfg.ParentID)
	}

	m.ToolCalls = turn.ToolCalls
	m.LLMCalls = turn.LLMCalls
	m.InputTokens = turn.InputTokens
	m.OutputTokens = turn.OutputTokens

	response := turn.ResponseText
	if err != nil && response == "" {
		response = errorResponse(err)
	}
	if response == "" {
		response = "Brain v3 finished but produced no text. (Session " + sessionID + ".)"
	}

	m.Success = err == nil && turn.ResponseText != ""
	m.TotalLatency = time.Since(start)
	return Result{Response: response, Metrics: m, ToolsUsed: turn.ToolsUsed}
}

// buildUserMessage prepends the <context> block (pinned + sender profile)
// and the sender's display name to the raw user content.
func buildUserMessage(preload PreloadedContext, senderName, content string) string {
	var b strings.Builder
	if r := preload.Render(senderName); r != "" {
		b.WriteString(r)
	}
	if senderName != "" {
		b.WriteString(senderName)
		b.WriteString(": ")
	}
	b.WriteString(content)
	return b.String()
}

func errorResponse(err error) string {
	return "Brain v3 couldn't complete this turn: " + err.Error()
}
