package brain3

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ToolCatalogHash returns a stable digest of the (name, description) pairs
// of the tools we'd send to Anthropic at agent-create time. Used to detect
// when the underlying Nexus tool catalog has changed so we can re-version
// the agent.
//
// We hash names + descriptions only (not full schemas) because schemas
// rarely change without an accompanying name/description tweak, and full-
// schema hashing would over-trigger updates on cosmetic changes.
func ToolCatalogHash(defs []brain.ToolDef) string {
	pairs := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.Function.Name == "" {
			continue
		}
		pairs = append(pairs, AnthropicToolName(d.Function.Name)+"\x00"+d.Function.Description)
	}
	sort.Strings(pairs) // order-independent
	h := sha256.Sum256([]byte(strings.Join(pairs, "\n")))
	return hex.EncodeToString(h[:8]) // 16-char hex digest, plenty for change detection
}

// reservedAgentToolNames are tool names Anthropic reserves for the built-in
// agent_toolset_20260401, regardless of whether that toolset is enabled on
// the agent. Custom tools using these names cause a 400 at agent-create time:
//
//	tools.N.custom.name: custom tool name "web_search" conflicts with agent tool name
//
// We rename conflicting Nexus tools transparently — the Anthropic schema
// gets "nexus_<name>", dispatch resolves back to "<name>" before calling
// s.executeTool(). The model sees the prefixed name; everything else is
// unchanged.
var reservedAgentToolNames = map[string]bool{
	"bash":       true,
	"read":       true,
	"write":      true,
	"edit":       true,
	"glob":       true,
	"grep":       true,
	"web_fetch":  true,
	"web_search": true,
}

const anthropicReservedPrefix = "nexus_"

// AnthropicToolName returns the name to use in the Anthropic agent schema
// for a given Nexus tool. Reserved names get the prefix; everything else
// passes through unchanged.
func AnthropicToolName(nexusName string) string {
	if reservedAgentToolNames[nexusName] {
		return anthropicReservedPrefix + nexusName
	}
	return nexusName
}

// NexusToolName reverses AnthropicToolName: given the name from an
// agent.custom_tool_use event, returns the original Nexus tool name to
// dispatch to s.executeTool().
func NexusToolName(anthropicName string) string {
	if strings.HasPrefix(anthropicName, anthropicReservedPrefix) {
		stripped := strings.TrimPrefix(anthropicName, anthropicReservedPrefix)
		if reservedAgentToolNames[stripped] {
			return stripped
		}
	}
	return anthropicName
}

// ConvertTools maps Nexus's v1/v2 tool catalog (brain.ToolDef) into the
// Anthropic Managed Agents custom-tool param shape. The agent will emit
// agent.custom_tool_use events for these; the v3 handler invokes the same
// s.executeTool() that v1/v2 use and sends back user.custom_tool_result.
//
// We deliberately do NOT enable agent_toolset_20260401 (Anthropic-hosted
// bash/file ops/web tools) in this phase — keeping tool semantics identical
// to v1/v2 means the same tool handlers, the same observability, the same
// security model. The hosted toolset can be opted into in a later phase.
func ConvertTools(defs []brain.ToolDef) []anthropic.BetaAgentNewParamsToolUnion {
	out := make([]anthropic.BetaAgentNewParamsToolUnion, 0, len(defs))
	for _, d := range defs {
		params := convertOne(d)
		if params == nil {
			continue
		}
		out = append(out, anthropic.BetaAgentNewParamsToolUnion{OfCustom: params})
	}
	return out
}

// convertOne turns a single Nexus ToolDef into Anthropic's custom-tool params.
// Returns nil if the tool's schema cannot be parsed (logged but non-fatal —
// the agent simply won't see that tool).
func convertOne(d brain.ToolDef) *anthropic.BetaManagedAgentsCustomToolParams {
	if d.Function.Name == "" || d.Function.Description == "" {
		return nil
	}

	props, required := parseSchema(d.Function.Parameters)
	if props == nil {
		props = map[string]any{}
	}

	return &anthropic.BetaManagedAgentsCustomToolParams{
		Name:        AnthropicToolName(d.Function.Name),
		Description: truncate(d.Function.Description, 1024),
		Type:        anthropic.BetaManagedAgentsCustomToolParamsTypeCustom,
		InputSchema: anthropic.BetaManagedAgentsCustomToolInputSchemaParam{
			Type:       anthropic.BetaManagedAgentsCustomToolInputSchemaTypeObject,
			Properties: props,
			Required:   required,
		},
	}
}

// parseSchema unmarshals a Nexus tool's JSON-Schema-shaped parameter blob and
// pulls out `properties` and `required`.
func parseSchema(raw json.RawMessage) (props map[string]any, required []string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil
	}
	if p, ok := schema["properties"].(map[string]any); ok {
		props = p
	}
	if r, ok := schema["required"].([]any); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	return props, required
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
