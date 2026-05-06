package brain3

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// AgentToolsetRevision is bumped whenever the agent_toolset config we send
// to Anthropic changes (which built-in tools are enabled/disabled). The
// revision is folded into ToolCatalogHash so any change auto-triggers
// applyToolsDriftIfNeeded on existing agents — no manual reset needed.
//
// We also bump it for unrelated agent-config changes (system prompt content,
// skill list) when we want existing agents to auto-pick-up the change. The
// "drift" path's Update call carries Tools but not System or Skills, so we
// extend it on bumps that need those — see applyToolsDriftIfNeeded.
//
// Revisions:
//   r1 — file tools enabled (read/write/edit/glob/grep).
//   r2 — system prompt mount-path fix (was hardcoded to /mnt/memory/brain;
//        now uses the actual /mnt/memory/<store-name> derived from the
//        memory_store name).
//   r3 — Operating Guide gains 'Task creation discipline' section: rules
//        that previously lived only in skill bodies (which load via
//        progressive disclosure and don't reliably fire) are now in the
//        always-loaded system prompt — multi-step → writing-plans,
//        explicit confirmation required for create_task, no auto-assign,
//        no precedent-overrides-rule.
//   r4 — Operating Guide gains 'Generating images' section. Workspace
//        default image model bumped to gemini-3.1-flash-image-preview
//        (Nano Banana 2). generate_image tool description sharpened for
//        Claude's compositional style; aspect_ratio enum added.
//   r5 — Personas section added to Operating Guide (Brain becomes
//        polymorphic). New custom skills creative-director + researcher
//        give Brain structured output contracts for visual/creative work
//        and external-information research. [skill:<Workflow>] tag and
//        <image-prompt> block conventions are now enforced via the
//        always-loaded system prompt.
//   r6 — r5 didn't actually fire — the older 'Generating images' section
//        was sufficient on its own and Claude followed it instead of the
//        new Personas guidance (which lived in a separate section that
//        read as optional). Replaced both with a unified Personas section
//        at top, and inlined the Ad Creative + Quick Research output
//        templates so Claude has the structure even without the persona
//        skill body loaded (progressive disclosure is unreliable).
//   r7 — Researcher persona now harnesses the existing Grok-backed
//        Social Pulse feature: new list_social_pulses + get_social_pulse
//        tools expose the workspace's already-produced sentiment/theme/
//        key-post analyses to Brain. Researcher skill body Social Pulse
//        workflow rewritten around these tools (no new Grok provider
//        wiring needed; workspace already has xAI key for the panel).
//        V3OperatingGuide Researcher inline section gains a 'toolkit'
//        bullet list naming the new tools alongside web search, search_x,
//        and the workspace internal-search tools.
const AgentToolsetRevision = "r7"

// FileToolNames are the agent_toolset tool names we keep enabled.
// Everything else in the toolset is disabled by default_config.
var fileToolNames = []anthropic.BetaManagedAgentsAgentToolConfigParamsName{
	anthropic.BetaManagedAgentsAgentToolConfigParamsNameRead,
	anthropic.BetaManagedAgentsAgentToolConfigParamsNameWrite,
	anthropic.BetaManagedAgentsAgentToolConfigParamsNameEdit,
	anthropic.BetaManagedAgentsAgentToolConfigParamsNameGlob,
	anthropic.BetaManagedAgentsAgentToolConfigParamsNameGrep,
}

// fileToolsToolsetParams returns the agent_toolset config that enables only
// the file tools. Used at agent-create and on update.
func fileToolsToolsetParams() *anthropic.BetaManagedAgentsAgentToolset20260401Params {
	configs := make([]anthropic.BetaManagedAgentsAgentToolConfigParams, 0, len(fileToolNames))
	for _, name := range fileToolNames {
		configs = append(configs, anthropic.BetaManagedAgentsAgentToolConfigParams{
			Name:    name,
			Enabled: param.NewOpt(true),
		})
	}
	return &anthropic.BetaManagedAgentsAgentToolset20260401Params{
		Type: anthropic.BetaManagedAgentsAgentToolset20260401ParamsTypeAgentToolset20260401,
		DefaultConfig: anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParams{
			Enabled: param.NewOpt(false),
		},
		Configs: configs,
	}
}

// BuildAgentTools assembles the full create-time tool list: Nexus customs +
// the agent_toolset with file tools enabled. The toolset gives Claude the
// read/write/edit/glob/grep needed to interact with the memory_store mount
// at /mnt/memory/brain/.
func BuildAgentTools(defs []brain.ToolDef) []anthropic.BetaAgentNewParamsToolUnion {
	customs := ConvertTools(defs)
	out := make([]anthropic.BetaAgentNewParamsToolUnion, 0, len(customs)+1)
	out = append(out, anthropic.BetaAgentNewParamsToolUnion{
		OfAgentToolset20260401: fileToolsToolsetParams(),
	})
	out = append(out, customs...)
	return out
}

// BuildAgentUpdateTools is the same shape as BuildAgentTools but for the
// update endpoint, which uses BetaAgentUpdateParamsToolUnion instead of
// BetaAgentNewParamsToolUnion. Used by applyToolsDriftIfNeeded.
func BuildAgentUpdateTools(defs []brain.ToolDef) []anthropic.BetaAgentUpdateParamsToolUnion {
	out := make([]anthropic.BetaAgentUpdateParamsToolUnion, 0, len(defs)+1)
	out = append(out, anthropic.BetaAgentUpdateParamsToolUnion{
		OfAgentToolset20260401: fileToolsToolsetParams(),
	})
	for _, d := range defs {
		if v1v2OnlyToolNames[d.Function.Name] {
			continue // v3 uses memory_store, not brain_memories
		}
		c := convertOne(d)
		if c == nil {
			continue
		}
		out = append(out, anthropic.BetaAgentUpdateParamsToolUnion{OfCustom: c})
	}
	return out
}

// ToolCatalogHash returns a stable digest of the (name, description) pairs
// of the Nexus tool catalog plus the AgentToolsetRevision plus the custom-
// skills catalog. Used to detect when any of those change so we can
// re-version the agent automatically.
//
// We hash tool names + descriptions only (not full schemas) because schemas
// rarely change without an accompanying name/description tweak, and full-
// schema hashing would over-trigger updates on cosmetic changes. The
// AgentToolsetRevision string is appended so flipping built-in tools on/off
// also fires drift. Custom-skill names are appended so adding a new skill
// to CustomSkills triggers a drift update on existing agents.
func ToolCatalogHash(defs []brain.ToolDef) string {
	pairs := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.Function.Name == "" || v1v2OnlyToolNames[d.Function.Name] {
			continue
		}
		pairs = append(pairs, AnthropicToolName(d.Function.Name)+"\x00"+d.Function.Description)
	}
	sort.Strings(pairs) // order-independent
	digestInput := strings.Join(pairs, "\n") +
		"\n!toolset:" + AgentToolsetRevision +
		"\n!skills:" + CustomSkillsCatalogDigest()
	h := sha256.Sum256([]byte(digestInput))
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

// v1v2OnlyToolNames are Nexus tools that were designed against the v1/v2
// brain_memories table and don't belong in the v3 catalog. v3 has its own
// memory layer (Anthropic memory_store, mounted in the session container)
// and exposing these tools would let Claude write to the wrong place — e.g.
// the trace from v3's first decision-log test showed Claude picking
// `save_memory` (v1/v2's brain_memories writer) instead of `write` (file
// tool against memory_store) because the name better matched user intent.
//
// Filter these from the catalog at conversion time.
var v1v2OnlyToolNames = map[string]bool{
	"save_memory":   true,
	"recall_memory": true,
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
		if v1v2OnlyToolNames[d.Function.Name] {
			continue // v3 uses memory_store, not brain_memories
		}
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
