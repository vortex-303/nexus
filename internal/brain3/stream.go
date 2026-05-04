package brain3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// turnResult holds the assembled output of a single send-and-stream cycle.
type turnResult struct {
	ResponseText string
	ToolsUsed    []string
	ToolCalls    int
	InputTokens  int
	OutputTokens int
	Terminated   bool
}

// runTurn sends a user message to an existing session and consumes the
// resulting event stream until the session goes idle (final answer) or
// terminates. Custom tool calls are bridged back through cfg.ExecuteTool —
// the same handler v1/v2 use — and returned as user.custom_tool_result.
//
// This is the steady-state v3 turn. Phase-2 scope: buffer-and-send-once
// (no streaming chat updates to WebSocket). Streaming UI is a follow-up.
func runTurn(ctx context.Context, client *anthropic.Client, sessionID string, userMessage string, cfg PipelineConfig) (turnResult, error) {
	if sessionID == "" {
		return turnResult{}, errors.New("brain3: empty session id")
	}

	// Open the stream BEFORE sending so we don't miss early events.
	// (Per managed-agents-events.md "stream-first ordering" guidance.)
	stream := client.Beta.Sessions.Events.StreamEvents(ctx, sessionID, anthropic.BetaSessionEventStreamParams{})
	defer stream.Close()

	// Send the user message now that the stream is buffering. We build the
	// params struct directly because the SDK's
	// BetaManagedAgentsEventParamsOfUserMessage helper doesn't set the
	// required Type discriminator and the API rejects the event with
	// "events[0].type: Field required".
	_, err := client.Beta.Sessions.Events.Send(ctx, sessionID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{
			{
				OfUserMessage: &anthropic.BetaManagedAgentsUserMessageEventParams{
					Type: anthropic.BetaManagedAgentsUserMessageEventParamsTypeUserMessage,
					Content: []anthropic.BetaManagedAgentsUserMessageEventParamsContentUnion{
						{OfText: &anthropic.BetaManagedAgentsTextBlockParam{Text: userMessage, Type: anthropic.BetaManagedAgentsTextBlockTypeText}},
					},
				},
			},
		},
	})
	if err != nil {
		return turnResult{}, fmt.Errorf("send user message: %w", err)
	}

	return consumeStream(ctx, client, sessionID, stream, cfg)
}

// consumeStream iterates events until the session reaches a terminal idle
// state or terminates. Custom tool calls are dispatched inline; tool results
// are sent back via events.Send, then the loop continues consuming.
func consumeStream(
	ctx context.Context,
	client *anthropic.Client,
	sessionID string,
	stream interface {
		Next() bool
		Current() anthropic.BetaManagedAgentsStreamSessionEventsUnion
		Err() error
		Close() error
	},
	cfg PipelineConfig,
) (turnResult, error) {
	var (
		out          turnResult
		responseBuf  strings.Builder
		toolsUsedSet = map[string]struct{}{}
	)

	for stream.Next() {
		ev := stream.Current()

		switch ev.Type {
		case "agent.message":
			// Final assistant text. Append every text block.
			for _, blk := range ev.Content.OfBetaManagedAgentsTextBlockArray {
				if blk.Text != "" {
					responseBuf.WriteString(blk.Text)
				}
			}

		case "agent.custom_tool_use":
			// Bridge to Nexus's existing tool handler. Track the Nexus name
			// so traces/logs match v1/v2 conventions.
			toolsUsedSet[NexusToolName(ev.Name)] = struct{}{}
			out.ToolCalls++

			result := dispatchCustomTool(ev, cfg)

			if err := sendToolResult(ctx, client, sessionID, ev.ID, result); err != nil {
				return out, fmt.Errorf("send tool result: %w", err)
			}

		case "agent.tool_use", "agent.mcp_tool_use":
			// Built-in or MCP tool — Anthropic-side execution. Only counted.
			toolsUsedSet[ev.Name] = struct{}{}
			out.ToolCalls++

		case "span.model_request_end":
			out.InputTokens += int(ev.ModelUsage.InputTokens)
			out.OutputTokens += int(ev.ModelUsage.OutputTokens)

		case "session.status_idle":
			// Idle is transient if the agent is waiting on a custom_tool_result.
			// requires_action means we're mid-flight; any other reason is terminal.
			stopType := stopReasonType(ev.StopReason)
			if stopType == "requires_action" {
				continue
			}
			// end_turn or retries_exhausted — done.
			out.ResponseText = responseBuf.String()
			out.ToolsUsed = setToSlice(toolsUsedSet)
			return out, nil

		case "session.status_terminated":
			out.Terminated = true
			out.ResponseText = responseBuf.String()
			out.ToolsUsed = setToSlice(toolsUsedSet)
			return out, nil

		case "session.error":
			// Soft surface — the SDK still streams these as events. Capture and
			// keep consuming; if the session also terminates we'll exit above.
			out.ResponseText = responseBuf.String()
			if msg := errorMessage(ev.Error); msg != "" && out.ResponseText == "" {
				out.ResponseText = "Brain v3 hit a session error: " + msg
			}
		}

		_ = ctx // ctx is honored by the SDK reader; this keeps the import obvious
	}

	if err := stream.Err(); err != nil {
		out.ResponseText = responseBuf.String()
		out.ToolsUsed = setToSlice(toolsUsedSet)
		return out, fmt.Errorf("stream: %w", err)
	}

	// Stream ended without an explicit idle — treat the buffered text as final.
	out.ResponseText = responseBuf.String()
	out.ToolsUsed = setToSlice(toolsUsedSet)
	return out, nil
}

// dispatchCustomTool invokes the same s.executeTool that v1/v2 use, with
// the agent.custom_tool_use event's input mapped into a brain.ToolCall shape.
// Returns the tool's text output (or an error string the agent can read).
func dispatchCustomTool(ev anthropic.BetaManagedAgentsStreamSessionEventsUnion, cfg PipelineConfig) string {
	if cfg.ExecuteTool == nil {
		return "Error: tool execution is not wired in this v3 pipeline."
	}

	// Anthropic's `input` field is `any`; v1/v2's executor expects a JSON
	// string of arguments. Marshal back to JSON for handler compatibility.
	var argsJSON string
	if ev.Input != nil {
		if b, err := json.Marshal(ev.Input); err == nil {
			argsJSON = string(b)
		} else {
			argsJSON = "{}"
		}
	} else {
		argsJSON = "{}"
	}

	// Reverse the AnthropicToolName rename so s.executeTool sees the
	// original Nexus tool name (e.g. "web_search", not "nexus_web_search").
	call := brain.ToolCall{
		ID:   ev.ID,
		Type: "function",
	}
	call.Function.Name = NexusToolName(ev.Name)
	call.Function.Arguments = argsJSON

	// senderMemberID is "" for now; v3 doesn't have it in scope yet (see
	// brain3.go TODO). Tools that need it gracefully degrade — same as
	// brain2's reflector behavior today.
	return cfg.ExecuteTool(cfg.Slug, cfg.ChannelID, "", call)
}

// sendToolResult posts a user.custom_tool_result event back to the session.
// The agent picks it up and resumes generation.
func sendToolResult(ctx context.Context, client *anthropic.Client, sessionID, customToolUseID, result string) error {
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := client.Beta.Sessions.Events.Send(sendCtx, sessionID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{
			toolResultEvent(customToolUseID, result),
		},
	})
	return err
}

// toolResultEvent builds a user.custom_tool_result event with a single text
// content block holding the tool's stdout-equivalent output. We set the Type
// discriminator explicitly because the SDK's OfUserCustomToolResult helper
// doesn't, and the API requires it.
func toolResultEvent(customToolUseID, result string) anthropic.BetaManagedAgentsEventParamsUnion {
	out := anthropic.BetaManagedAgentsEventParamsOfUserCustomToolResult(customToolUseID)
	if out.OfUserCustomToolResult != nil {
		out.OfUserCustomToolResult.Type = anthropic.BetaManagedAgentsUserCustomToolResultEventParamsTypeUserCustomToolResult
		out.OfUserCustomToolResult.Content = []anthropic.BetaManagedAgentsUserCustomToolResultEventParamsContentUnion{
			{OfText: &anthropic.BetaManagedAgentsTextBlockParam{Text: result, Type: anthropic.BetaManagedAgentsTextBlockTypeText}},
		}
		out.OfUserCustomToolResult.IsError = param.NewOpt(false)
	}
	return out
}

// stopReasonType extracts the discriminator from a status_idle stop_reason
// union. We use it to distinguish transient idles (requires_action) from
// terminal ones (end_turn / retries_exhausted).
func stopReasonType(sr anthropic.BetaManagedAgentsSessionStatusIdleEventStopReasonUnion) string {
	// The union exposes a Type field on each variant; the flat union's
	// JSON form has "type" populated for us.
	type withType struct {
		Type string `json:"type"`
	}
	var wt withType
	if b, err := json.Marshal(sr); err == nil {
		_ = json.Unmarshal(b, &wt)
	}
	return wt.Type
}

// errorMessage extracts a human-readable message from a session.error event's
// error union, best-effort.
func errorMessage(eu anthropic.BetaManagedAgentsSessionErrorEventErrorUnion) string {
	type withMessage struct {
		Message string `json:"message"`
	}
	var wm withMessage
	if b, err := json.Marshal(eu); err == nil {
		_ = json.Unmarshal(b, &wm)
	}
	return wm.Message
}

func setToSlice(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
