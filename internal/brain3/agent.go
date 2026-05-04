package brain3

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// ErrNoAPIKey is returned when v3 is requested but no Anthropic API key is
// configured for the workspace. Callers should fall back to v2 for the turn.
var ErrNoAPIKey = errors.New("brain3: anthropic_api_key is not set for this workspace")

// AgentInfo is the minimal record of a provisioned Anthropic agent +
// environment + memory store. The IDs are persisted in brain_settings
// (mga_agent_id, mga_agent_version, mga_environment_id, mga_memory_store_id)
// so subsequent runs reuse them.
type AgentInfo struct {
	AgentID       string
	AgentVersion  int64
	EnvironmentID string
	MemoryStoreID string
}

// SettingsStore abstracts the brain_settings read/write operations the v3
// pipeline needs. The server package implements this against its existing
// getBrainSetting / setBrainSetting helpers.
type SettingsStore interface {
	Get(slug, key string) string
	Set(slug, key, value string) error
}

// NewClient constructs an Anthropic SDK client with the per-workspace API key.
func NewClient(apiKey string) (*anthropic.Client, error) {
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &c, nil
}

// EnsureProvisioned returns AgentInfo for the workspace, lazily creating the
// Anthropic environment + agent if they are not already recorded in
// brain_settings. Idempotent: subsequent calls reuse the persisted IDs.
//
// When the persisted IDs are missing or stale (404 on retrieve), it
// re-provisions and overwrites. Tool catalog drift is NOT auto-detected here
// — that's a Phase-1.5 follow-up (hash the tool list, bump agent version on
// change).
func EnsureProvisioned(ctx context.Context, settings SettingsStore, slug string, systemPrompt string, tools []brain.ToolDef) (AgentInfo, error) {
	apiKey := settings.Get(slug, "anthropic_api_key")
	client, err := NewClient(apiKey)
	if err != nil {
		return AgentInfo{}, err
	}

	envID := settings.Get(slug, "mga_environment_id")
	agentID := settings.Get(slug, "mga_agent_id")
	versionStr := settings.Get(slug, "mga_agent_version")
	memID := settings.Get(slug, "mga_memory_store_id")

	// Fast path: all IDs persisted, return without API roundtrips.
	if envID != "" && agentID != "" && memID != "" {
		var v int64
		_, _ = fmt.Sscanf(versionStr, "%d", &v)
		return AgentInfo{AgentID: agentID, AgentVersion: v, EnvironmentID: envID, MemoryStoreID: memID}, nil
	}

	// Slow path: provision whichever pieces are missing. Order matters —
	// memory_store before agent, so the addendum it teaches the agent about
	// is genuinely backed by a real store ID at session-create time.
	if memID == "" {
		mid, err := ensureMemoryStore(ctx, client, settings, slug)
		if err != nil {
			return AgentInfo{}, err
		}
		memID = mid
	}

	if envID == "" {
		env, err := createEnvironment(ctx, client, slug)
		if err != nil {
			return AgentInfo{}, fmt.Errorf("create environment: %w", err)
		}
		envID = env.ID
		if err := settings.Set(slug, "mga_environment_id", envID); err != nil {
			return AgentInfo{}, fmt.Errorf("persist environment id: %w", err)
		}
	}

	if agentID == "" {
		agent, err := createAgent(ctx, client, slug, systemPrompt, tools)
		if err != nil {
			return AgentInfo{}, fmt.Errorf("create agent: %w", err)
		}
		agentID = agent.ID
		if err := settings.Set(slug, "mga_agent_id", agentID); err != nil {
			return AgentInfo{}, fmt.Errorf("persist agent id: %w", err)
		}
		if err := settings.Set(slug, "mga_agent_version", fmt.Sprintf("%d", agent.Version)); err != nil {
			return AgentInfo{}, fmt.Errorf("persist agent version: %w", err)
		}
		return AgentInfo{AgentID: agent.ID, AgentVersion: agent.Version, EnvironmentID: envID, MemoryStoreID: memID}, nil
	}

	var v int64
	_, _ = fmt.Sscanf(versionStr, "%d", &v)
	return AgentInfo{AgentID: agentID, AgentVersion: v, EnvironmentID: envID, MemoryStoreID: memID}, nil
}

// createEnvironment provisions a new Anthropic cloud environment for the
// workspace. Uses unrestricted networking by default; future work may switch
// to a per-workspace allow-list (see managed-agents-environments.md
// "package_managers_and_custom").
func createEnvironment(ctx context.Context, client *anthropic.Client, slug string) (*anthropic.BetaEnvironment, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.Beta.Environments.New(ctx, anthropic.BetaEnvironmentNewParams{
		Name:        EnvironmentName(slug),
		Description: param.NewOpt("Nexus Brain v3 environment for workspace " + slug),
		Config: anthropic.BetaCloudConfigParams{
			Networking: anthropic.BetaCloudConfigParamsNetworkingUnion{
				OfUnrestricted: &anthropic.BetaUnrestrictedNetworkParam{},
			},
		},
	})
}

// createAgent provisions the workspace's Anthropic agent with Nexus's tool
// catalog mapped to custom tools. The system prompt is captured at this
// point — to evolve it, bump the agent version (Phase 1.5).
func createAgent(ctx context.Context, client *anthropic.Client, slug, systemPrompt string, tools []brain.ToolDef) (*anthropic.BetaManagedAgentsAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	model := anthropic.BetaManagedAgentsModel(DefaultModel)

	params := anthropic.BetaAgentNewParams{
		Name: AgentName(slug),
		Model: anthropic.BetaManagedAgentsModelConfigParams{
			ID: model,
		},
		Description: param.NewOpt("Brain v3 (Claude Managed Agent) for Nexus workspace " + slug),
		Tools:       ConvertTools(tools),
	}
	// Append the v3 memory addendum so Claude knows the layout, the in-turn
	// write discipline, and the <context> pre-injection convention. Captured
	// at agent-create time; updates require a version bump.
	if systemPrompt != "" {
		params.System = param.NewOpt(AppendMemoryAddendum(systemPrompt))
	} else {
		params.System = param.NewOpt(AppendMemoryAddendum(""))
	}

	return client.Beta.Agents.New(ctx, params)
}
