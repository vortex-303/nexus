package brain3

import (
	"context"
	"database/sql"
	"errors"
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
	// Capabilities is the markdown block describing what Brain can do in
	// this workspace right now (engine + services + MCP). Computed by
	// the server-side handler via Server.BuildCapabilitiesSection so this
	// package doesn't depend on internal/server. Injected into the per-
	// turn pre-injected context block alongside Model + Pinned + sender
	// profile.
	Capabilities string
	// Images are recent channel images the user shared, attached to the
	// user message as image content blocks. Claude Sonnet 4.6 / Opus 4.7
	// read them natively. Server-side handler builds these via
	// getRecentChannelImages (same source v2 uses for vision); the v3
	// pipeline converts them into Anthropic's BetaManagedAgentsImageBlock
	// shape inside runTurn. Empty for non-image turns.
	Images []brain.MessageImage
	// OnTextDelta is called for every agent.message text block as it arrives
	// on the SSE stream. Optional — if nil, streaming is disabled and the
	// caller gets only the final aggregated text in Result.Response.
	OnTextDelta func(delta string)
	// OnToolStart fires when Brain begins executing a tool. The toolName is
	// the Nexus-side name (already de-prefixed for reserved names). The
	// server uses this to broadcast a "tool_executing" agent state so users
	// see "Brain is searching the web..." / "...generating image..." etc.
	OnToolStart func(toolName string)
	// OnToolEnd fires when a tool finishes (success or error). The server
	// uses this to flip the indicator back to "thinking" between tool calls.
	OnToolEnd func(toolName string)
}

// Result is the output of a Brain v3 turn.
type Result struct {
	Response       string
	Metrics        Metrics
	ToolsUsed      []string
	DecisionWrites []DecisionWrite // /decisions/*.md writes the agent made this turn
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
	info, err := EnsureProvisioned(ctx, cfg.Settings, cfg.DB, cfg.Slug, cfg.SystemPrompt, cfg.AllTools)
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

	// One-shot: seed /people/<slug>.md for existing workspace members so the
	// agent doesn't start blind. Idempotent on the per-file level (won't
	// overwrite existing profiles), and gated by a workspace-level flag so
	// we don't repeat the (cheap but pointless) check every turn.
	if cfg.Settings.Get(cfg.Slug, "mga_members_seeded") != "true" && info.MemoryStoreID != "" {
		if err := SeedMemberProfiles(ctx, client, cfg.DB, info.MemoryStoreID); err == nil {
			_ = cfg.Settings.Set(cfg.Slug, "mga_members_seeded", "true")
		}
		// Best-effort; if seeding errors we'll retry next turn.
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
	// Runtime ground truth — names the active Anthropic model in the per-turn
	// pre-injected context block so the agent doesn't parrot earlier turns
	// when the model has changed (drift detection auto-bumps agent versions
	// faster than the agent can notice from its own internals).
	preload.Model = resolveModel(cfg.Settings, cfg.Slug)
	// Capabilities — engine + service + MCP awareness, computed by the
	// server-side handler (since this package can't depend on internal/server).
	preload.Capabilities = cfg.Capabilities

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
	return Result{Response: response, Metrics: m, ToolsUsed: turn.ToolsUsed, DecisionWrites: turn.DecisionWrites}
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

// errorResponse turns a pipeline error into a chat-visible message. The
// Anthropic SDK surfaces auth + rate-limit + config issues as wrapped HTTP
// errors whose .Error() string is verbose and includes the raw URL + request
// ID — fine for logs, but unhelpful as a chat reply. We special-case the
// failure modes a workspace admin can act on, so the message points at the
// fix instead of dumping a stack trace.
func errorResponse(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, ErrNoAPIKey):
		return "Brain (Claude) isn't connected yet. An admin can add an Anthropic API key in **Settings → Brain → API Keys**, or switch the engine to OpenRouter."
	case isAuthError(msg):
		return "Brain's Anthropic API key isn't working — Anthropic returned 401 Unauthorized. An admin can re-enter the key in **Settings → Brain → API Keys**, or switch the engine to OpenRouter as a quick fallback."
	case isSkillsNotFound(msg):
		return "Brain can't reach Anthropic's Skills API on this account — the request 404'd. The Skills beta isn't enabled on every Anthropic account yet. Easiest path: switch the engine to **OpenRouter** in Settings → Brain → Engine, or use a different Anthropic account that has Skills + Managed Agents access."
	case isDisplayTitleCollision(msg):
		return "Brain couldn't bootstrap its skills — Anthropic reports a display-title collision (the same Claude API key is already in use by another Nexus workspace). This is usually a stale-cache issue; an admin can click **Reset Brain Agent** in Settings → Brain to re-provision."
	case isRateLimitError(msg):
		return "Anthropic is rate-limiting this workspace right now. Try again in a minute, or switch the engine to OpenRouter in **Settings → Brain → Engine**."
	}
	return "Brain v3 couldn't complete this turn: " + msg
}

func isAuthError(msg string) bool {
	return strings.Contains(msg, "401") || strings.Contains(msg, "authentication_error") || strings.Contains(msg, "Authentication failed")
}

func isDisplayTitleCollision(msg string) bool {
	return strings.Contains(msg, "cannot reuse an existing display_title")
}

func isRateLimitError(msg string) bool {
	return strings.Contains(msg, "429") || strings.Contains(msg, "rate_limit")
}

// isSkillsNotFound matches the 404 Anthropic returns when the Skills /
// Managed Agents beta isn't available on the account behind the API key.
// The error string from the SDK looks like:
//   POST "https://api.anthropic.com/v1/skills?beta=true": 404 Not Found
//   {"type":"not_found_error","message":"Not found"}
// We look at both the path (skills/agents/memory_stores) and the 404 to
// avoid false positives on legitimate 404s from other endpoints.
func isSkillsNotFound(msg string) bool {
	if !strings.Contains(msg, "404") {
		return false
	}
	return strings.Contains(msg, "/v1/skills") ||
		strings.Contains(msg, "/v1/agents") ||
		strings.Contains(msg, "/v1/memory_stores") ||
		strings.Contains(msg, "/v1/environments")
}
