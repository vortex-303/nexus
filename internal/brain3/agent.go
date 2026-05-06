package brain3

import (
	"context"
	"database/sql"
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
func EnsureProvisioned(ctx context.Context, settings SettingsStore, db *sql.DB, slug string, systemPrompt string, tools []brain.ToolDef) (AgentInfo, error) {
	apiKey := settings.Get(slug, "anthropic_api_key")
	client, err := NewClient(apiKey)
	if err != nil {
		return AgentInfo{}, err
	}

	envID := settings.Get(slug, "mga_environment_id")
	agentID := settings.Get(slug, "mga_agent_id")
	versionStr := settings.Get(slug, "mga_agent_version")
	memID := settings.Get(slug, "mga_memory_store_id")

	// Fast path: all IDs persisted. Still check for drift — if the user
	// changed model or system-prompt template in settings, we bump the
	// agent's version so new sessions pick the change up.
	//
	// Sessions are pinned to the agent version at create time (Anthropic
	// behavior); existing sessions keep running the version they were
	// born on even after the agent is updated. So whenever ANY drift
	// detector fires, we clear brain_managed_sessions — the in-flight
	// turn (and every future turn) creates a fresh session that pulls
	// the latest agent version. Without this, persona/template/tool
	// changes wouldn't reach existing chats until the user manually
	// clicked Reset Agent.
	if envID != "" && agentID != "" && memID != "" {
		var v int64
		_, _ = fmt.Sscanf(versionStr, "%d", &v)
		info := AgentInfo{AgentID: agentID, AgentVersion: v, EnvironmentID: envID, MemoryStoreID: memID}
		preVersion := info.AgentVersion
		info, err := applyModelDriftIfNeeded(ctx, client, settings, slug, info)
		if err != nil {
			return info, err
		}
		info, err = applyToolsDriftIfNeeded(ctx, client, settings, slug, systemPrompt, tools, info)
		if err != nil {
			return info, err
		}
		info, err = applyTemplateDriftIfNeeded(ctx, client, settings, slug, systemPrompt, info)
		if err != nil {
			return info, err
		}
		if info.AgentVersion != preVersion && db != nil {
			// Drift fired (any of the three). Clear stale session rows so
			// the current turn re-creates against the new agent version.
			_, _ = db.Exec("DELETE FROM brain_managed_sessions")
		}
		return info, nil
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
		agent, err := createAgent(ctx, client, settings, slug, systemPrompt, tools)
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
		// Record the model + template the agent was created with so the
		// drift detectors can fire when settings change later.
		if err := settings.Set(slug, "mga_provisioned_model", resolveModel(settings, slug)); err != nil {
			return AgentInfo{}, fmt.Errorf("persist provisioned model: %w", err)
		}
		if err := settings.Set(slug, "mga_provisioned_template", resolveSystemPromptTemplate(settings, slug)); err != nil {
			return AgentInfo{}, fmt.Errorf("persist provisioned template: %w", err)
		}
		if err := settings.Set(slug, "mga_provisioned_tools_hash", ToolCatalogHash(tools)); err != nil {
			return AgentInfo{}, fmt.Errorf("persist provisioned tools hash: %w", err)
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

// DefaultAnthropicSkills are the pre-built skills v3 enables on every agent
// by default. Each gives the agent native handling of a common file format —
// users frequently drop these into Nexus channels and Brain becomes
// substantially more useful when it can read/edit them properly.
//
// Cost is "free" in the sense that skills load on-demand (only the
// description sits in the agent's awareness; full content is pulled when
// the task warrants it).
var DefaultAnthropicSkills = []string{"docx", "xlsx", "pdf", "pptx"}

// resolveModel returns the workspace's chosen Anthropic model from
// brain_settings.mga_model, falling back to DefaultModel. Validates against a
// short allowlist so a fat-fingered setting doesn't 400 at agent-create time.
func resolveModel(settings SettingsStore, slug string) string {
	chosen := settings.Get(slug, "mga_model")
	switch chosen {
	case "claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5":
		return chosen
	}
	return DefaultModel
}

// createAgent provisions the workspace's Anthropic agent with Nexus's tool
// catalog mapped to custom tools. The system prompt is captured at this
// point — to evolve it, bump the agent version (Phase 1.5).
func createAgent(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug, systemPrompt string, tools []brain.ToolDef) (*anthropic.BetaManagedAgentsAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	model := anthropic.BetaManagedAgentsModel(resolveModel(settings, slug))

	// Pre-built Anthropic skills. Set Type explicitly because the SDK helper
	// doesn't (same pattern as the user.message Type discriminator bug).
	skills := make([]anthropic.BetaManagedAgentsSkillParamsUnion, 0, len(DefaultAnthropicSkills)+len(CustomSkills))
	for _, id := range DefaultAnthropicSkills {
		skills = append(skills, anthropic.BetaManagedAgentsSkillParamsUnion{
			OfAnthropic: &anthropic.BetaManagedAgentsAnthropicSkillParams{
				SkillID: id,
				Type:    anthropic.BetaManagedAgentsAnthropicSkillParamsTypeAnthropic,
			},
		})
	}
	// Custom skills uploaded to this workspace's Anthropic org. Idempotent —
	// EnsureCustomSkills only uploads skills not already cached in
	// brain_settings.
	customIDs, err := EnsureCustomSkills(ctx, client, settings, slug)
	if err != nil {
		return nil, fmt.Errorf("ensure custom skills: %w", err)
	}
	for _, name := range CustomSkillNamesSorted() {
		id, ok := customIDs[name]
		if !ok || id == "" {
			continue
		}
		skills = append(skills, anthropic.BetaManagedAgentsSkillParamsUnion{
			OfCustom: &anthropic.BetaManagedAgentsCustomSkillParams{
				SkillID: id,
				Type:    anthropic.BetaManagedAgentsCustomSkillParamsTypeCustom,
			},
		})
	}

	params := anthropic.BetaAgentNewParams{
		Name: AgentName(slug),
		Model: anthropic.BetaManagedAgentsModelConfigParams{
			ID: model,
		},
		Description: param.NewOpt("Brain v3 (Claude Managed Agent) for Nexus workspace " + slug),
		// Custom Nexus tools + agent_toolset with file tools enabled. The
		// file tools (read/write/edit/glob/grep) are what give Claude the
		// ability to write to the memory_store mount at /mnt/memory/brain/.
		Tools:  BuildAgentTools(tools),
		Skills: skills,
	}
	// Compose the system prompt via the chosen template (workspace voice +
	// optional v3 Operating Guide + memory addendum). Captured at agent-create
	// time; template changes are picked up on subsequent turns by
	// applyTemplateDriftIfNeeded.
	params.System = param.NewOpt(ResolveSystemPrompt(settings, slug, systemPrompt))

	return client.Beta.Agents.New(ctx, params)
}

// applyModelDriftIfNeeded compares the user's chosen model (mga_model) to the
// model that was active when the agent was last provisioned (mga_provisioned_model).
// If they differ, it calls Beta.Agents.Update to bump the agent's version with
// the new model. New sessions will pin to the latest version automatically;
// existing sessions keep running on whatever version they were created with.
//
// Cost: one ~200-500ms API call on the first message after a model change.
// No-op on every subsequent message until the user changes the setting again.
func applyModelDriftIfNeeded(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug string, info AgentInfo) (AgentInfo, error) {
	desired := resolveModel(settings, slug)
	provisioned := settings.Get(slug, "mga_provisioned_model")
	if desired == provisioned || info.AgentID == "" {
		// First-ever provision will be reconciled below in the create path.
		return info, nil
	}

	updateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	updated, err := client.Beta.Agents.Update(updateCtx, info.AgentID, anthropic.BetaAgentUpdateParams{
		Version: info.AgentVersion,
		Model: anthropic.BetaManagedAgentsModelConfigParams{
			ID: anthropic.BetaManagedAgentsModel(desired),
		},
	})
	if err != nil {
		return info, fmt.Errorf("update agent model: %w", err)
	}
	info.AgentVersion = updated.Version
	if err := settings.Set(slug, "mga_agent_version", fmt.Sprintf("%d", updated.Version)); err != nil {
		return info, fmt.Errorf("persist agent version: %w", err)
	}
	if err := settings.Set(slug, "mga_provisioned_model", desired); err != nil {
		return info, fmt.Errorf("persist provisioned model: %w", err)
	}
	return info, nil
}

// applyToolsDriftIfNeeded compares the current Nexus tool catalog hash against
// the one captured at agent-create time. If they differ, calls
// Beta.Agents.Update with the freshly-converted tool list AND the resolved
// system prompt and bumps the agent version. We send System alongside Tools
// because the AgentToolsetRevision marker is also bumped for system-prompt
// content changes (e.g. mount-path fixes inside the addendum) — sending both
// keeps a single drift point that propagates whichever changed.
func applyToolsDriftIfNeeded(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug, basePrompt string, tools []brain.ToolDef, info AgentInfo) (AgentInfo, error) {
	desired := ToolCatalogHash(tools)
	provisioned := settings.Get(slug, "mga_provisioned_tools_hash")
	if desired == provisioned || info.AgentID == "" {
		return info, nil
	}

	updateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Make sure custom skills are uploaded before referencing them.
	customIDs, err := EnsureCustomSkills(updateCtx, client, settings, slug)
	if err != nil {
		return info, fmt.Errorf("ensure custom skills: %w", err)
	}

	// Compose the full Skills list (Anthropic pre-built + custom). Update is
	// a full replacement, so we need to send everything we want attached.
	skills := make([]anthropic.BetaManagedAgentsSkillParamsUnion, 0, len(DefaultAnthropicSkills)+len(customIDs))
	for _, id := range DefaultAnthropicSkills {
		skills = append(skills, anthropic.BetaManagedAgentsSkillParamsUnion{
			OfAnthropic: &anthropic.BetaManagedAgentsAnthropicSkillParams{
				SkillID: id,
				Type:    anthropic.BetaManagedAgentsAnthropicSkillParamsTypeAnthropic,
			},
		})
	}
	for _, name := range CustomSkillNamesSorted() {
		id, ok := customIDs[name]
		if !ok || id == "" {
			continue
		}
		skills = append(skills, anthropic.BetaManagedAgentsSkillParamsUnion{
			OfCustom: &anthropic.BetaManagedAgentsCustomSkillParams{
				SkillID: id,
				Type:    anthropic.BetaManagedAgentsCustomSkillParamsTypeCustom,
			},
		})
	}

	// BuildAgentUpdateTools includes both Nexus customs and the
	// agent_toolset (file tools).
	resp, err := client.Beta.Agents.Update(updateCtx, info.AgentID, anthropic.BetaAgentUpdateParams{
		Version: info.AgentVersion,
		Tools:   BuildAgentUpdateTools(tools),
		System:  param.NewOpt(ResolveSystemPrompt(settings, slug, basePrompt)),
		Skills:  skills,
	})
	if err != nil {
		return info, fmt.Errorf("update agent tools: %w", err)
	}
	info.AgentVersion = resp.Version
	if err := settings.Set(slug, "mga_agent_version", fmt.Sprintf("%d", resp.Version)); err != nil {
		return info, fmt.Errorf("persist agent version: %w", err)
	}
	if err := settings.Set(slug, "mga_provisioned_tools_hash", desired); err != nil {
		return info, fmt.Errorf("persist provisioned tools hash: %w", err)
	}
	return info, nil
}

// applyTemplateDriftIfNeeded compares the user's chosen system-prompt template
// (mga_system_prompt_template) to the one active when the agent was last
// provisioned (mga_provisioned_template). If they differ, calls
// Beta.Agents.Update with the freshly-composed system prompt and bumps the
// agent version. Same cost profile as applyModelDriftIfNeeded.
//
// Note: this only detects *template* changes (workspace ↔ v3-team-brain).
// Edits to SOUL.md / INSTRUCTIONS.md are NOT auto-detected — those require
// an explicit "Reset agent" action (clear mga_agent_id), since hashing the
// composed prompt on every turn is more cost than it's worth.
func applyTemplateDriftIfNeeded(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug, basePrompt string, info AgentInfo) (AgentInfo, error) {
	desired := resolveSystemPromptTemplate(settings, slug)
	provisioned := settings.Get(slug, "mga_provisioned_template")
	if desired == provisioned || info.AgentID == "" {
		return info, nil
	}

	updateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	composed := ResolveSystemPrompt(settings, slug, basePrompt)
	updated, err := client.Beta.Agents.Update(updateCtx, info.AgentID, anthropic.BetaAgentUpdateParams{
		Version: info.AgentVersion,
		System:  param.NewOpt(composed),
	})
	if err != nil {
		return info, fmt.Errorf("update agent system prompt: %w", err)
	}
	info.AgentVersion = updated.Version
	if err := settings.Set(slug, "mga_agent_version", fmt.Sprintf("%d", updated.Version)); err != nil {
		return info, fmt.Errorf("persist agent version: %w", err)
	}
	if err := settings.Set(slug, "mga_provisioned_template", desired); err != nil {
		return info, fmt.Errorf("persist provisioned template: %w", err)
	}
	return info, nil
}
