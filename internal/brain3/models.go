// Package brain3 implements Brain v3 — a Claude Managed Agents-backed brain.
//
// Architecture (per BRAIN_2_REPORT.md follow-up; see also v3 design notes):
//   - One Anthropic Agent per workspace, lazy-created on first v3 trigger.
//   - One Anthropic Session per (channel_id, parent_id) pair. Threads get
//     isolated sessions; the channel root is its own session.
//   - Nexus's existing tool catalog is exposed as Anthropic *custom tools* —
//     when the agent emits agent.custom_tool_use, the v3 handler invokes
//     the same s.executeTool() v1/v2 use, then sends user.custom_tool_result
//     back. Identical execution semantics to v1/v2.
//   - System prompt is captured at agent-create time. Per-turn dynamic
//     context (recent messages, pinned memories, attached images) is
//     prepended to the user message in a <context>...</context> block.
//
// Silo guarantee: this package does not import internal/brain2. It does
// import internal/brain for the shared ToolDef / Message types only —
// never calls v1 logic.
package brain3

import "time"

// Pipeline version label persisted in brain_traces.brain_version.
const VersionTag = "v3"

// AnthropicBetaHeader is the header value required for managed-agents calls.
// The Go SDK adds this automatically for client.Beta.{Agents,Sessions,Environments,...}
// methods, but we keep the constant here for documentation and any raw HTTP
// fallback we may need.
const AnthropicBetaHeader = "managed-agents-2026-04-01"

// Default model for v3 agents. Claude Opus 4.7 — adaptive thinking only,
// `xhigh` effort recommended for agentic workloads. Override via the
// brain_settings.model setting.
const DefaultModel = "claude-opus-4-7"

// EnvironmentName returns the deterministic Anthropic environment name for a
// workspace slug. Used so we can find an existing environment by name on
// re-provision after a manual delete.
func EnvironmentName(slug string) string {
	return "nexus-env-" + slug
}

// AgentName returns the deterministic Anthropic agent display name for a
// workspace slug.
func AgentName(slug string) string {
	return "Nexus Brain v3 (" + slug + ")"
}

// SessionRecord is the on-disk mapping between a Nexus channel-thread pair
// and an Anthropic session. Stored in brain_managed_sessions (migration 58).
type SessionRecord struct {
	ChannelID         string
	ParentID          string // empty string = channel root, non-empty = thread root message ID
	AnthropicSessionID string
	Status            string // "running" | "idle" | "terminated" | "rescheduling"
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastEventAt       time.Time
}

// IsActive reports whether the session can still be used to send events.
// Terminated sessions must be replaced with a fresh one.
func (s SessionRecord) IsActive() bool {
	return s.Status != "terminated"
}
