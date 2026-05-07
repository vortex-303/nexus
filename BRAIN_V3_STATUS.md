# Brain v3 — Status

> **Snapshot: 2026-05-07.** Branch: `brain-v3-claude-managed-agents`.
> Live at `nexusteams.dev` (test workspace `u0nsas` only).
> **Tagged `v3.0.0-beta.1`** + [PR #1](https://github.com/vortex-303/nexus/pull/1) open to `main`.
> Brain product framing reorganized into **two engines** (OpenRouter + Claude) with a unified Settings UX.

## Headline

Brain v3 — Claude Managed Agents-backed alternative to v1/v2 — is **end-to-end working in production**. A workspace owner toggles `brain_version='v3'`, configures an Anthropic API key, and gets:

- Claude Sonnet 4.6 (default), Haiku 4.5, Opus 4.7, or Opus 4.6 as the brain model
- Per-channel-thread persistent sessions (Anthropic-hosted)
- Workspace-scoped memory_store with 5 file types Claude reads/writes (`read`, `write`, `edit`, `glob`, `grep`)
- **9 skills attached**: 4 Anthropic pre-built (docx, xlsx, pdf, pptx) + 5 custom (decision-log, task-conventions, writing-plans, executing-plans, verification-before-completion) **+ 2 polymorphic personas** (creative-director, researcher) for deliverable-shaped requests
- **Image generation** via Google Nano Banana 2 (`gemini-3.1-flash-image-preview`) with aspect-ratio control, wired through `generate_image`
- **Live progress messaging** — chat indicator names the tool in flight ("Brain is using web search...", "...the image generator...")
- **Per-conversation turn-taking** — Brain behaves like a human teammate, one reply per (channel, parent_id) at a time, parallel across conversations
- Visible memory viewer panel showing what Claude has learned
- Cost + traces integrated with the existing `/usage` dashboard

v1 and v2 are completely untouched — opt-in per workspace, default off. Legacy `@Creative Director` + `@Caly` agents hidden from v3 workspaces (Brain absorbs their roles via personas).

---

## What's shipped (per turn-of-the-loop)

### Phase 0 — Scaffold + routing

- `internal/brain3/` package — separate from `internal/brain/` (v1) and `internal/brain2/` (v2)
- Anthropic SDK `v1.38.0` added
- Migration 58 (`brain_managed_sessions`), 59 + 60 (idempotent `brain_traces` + `brain_trace_steps`)
- `brain_settings` allowlist extended with `mga_*` keys + `mga_skill_*` prefix-allow
- `ws.go` routing switch: `brain_version='v3'` → `handleBrainV3`; default → v1
- `handleBrainV3` mirrors `handleBrainV2`'s shape (semaphore, staleness, thinking-state, system-prompt build, response send, action log)
- Reset Agent endpoint: `POST /api/workspaces/{slug}/brain/v3/reset-agent` (admin)
- Memory listing endpoint: `GET /api/workspaces/{slug}/brain/v3/memory` (admin)

### Phase 1 — Agent provisioning + tools + memory

- **Lazy provisioning** of Anthropic Agent + Environment + memory_store on first message per workspace; IDs cached in `brain_settings`
- **Per-(channel, parent_id) sessions** — channels and threads get isolated context; mapping in `brain_managed_sessions`
- **Tool catalog bridge** — Nexus's existing `getAllTools()` projected as Anthropic custom tools, with:
  - Reserved-name rename: `web_search` → `nexus_web_search` (avoids collision with Anthropic's built-in)
  - v1/v2-only memory tool filter: `save_memory`, `recall_memory` excluded from v3 (it has its own memory layer); also rejected at dispatch time as defense in depth
- **agent_toolset_20260401 with file tools enabled** — `read`, `write`, `edit`, `glob`, `grep`. `bash`, `web_fetch`, `web_search` remain disabled (security + we have custom equivalents)
- **memory_store FUSE-mounted** at `/mnt/memory/nexus-brain-<slug>/` inside the session container
- **Pre-injected context** — `pinned.md` + `people/<sender>.md` prepended to user messages in a `<context>...</context>` block
- **Members seed** — on first agent provision, `/people/<slug>.md` files written from existing `members` table data (title, bio, goals, reports_to). One-shot per workspace via `mga_members_seeded` flag
- **Knowledge tool exposure** — `search_knowledge` (Qdrant-backed v1/v2 tool) automatically available to v3 via shared `getAllTools` catalog

### Phase 2 — UI + observability

- **Brain Pipeline radio** in Brain Settings: v1 / v2 / v3 (BETA)
- **LLM Provider section** has Anthropic / Claude card with:
  - API key field (masked, write-only)
  - Model dropdown (Sonnet 4.6 default, Haiku 4.5, Opus 4.7, Opus 4.6) with drift warning
  - System prompt template dropdown (v3-team-brain default, Workspace) with drift warning
  - Provisioning panel (agent ID + version, env ID, memory store ID)
  - **Skills attached** pill row (built-in vs custom distinguished)
  - Reset Agent button
- **Engine Mode** auto-hides when v3 is active; replaced by single-line note
- **Sidebar pill** shows `Claude: Sonnet 4.6` (or chosen model) in Anthropic terracotta
- **Memory tab** has a top section "Memory Store · v3 · `/mnt/memory/...`" when v3 active — collapsible files grouped by top-level directory (people/, decisions/, projects/, etc.) with inline content preview + refresh button
- **Trace persistence** — every v3 turn writes `brain_traces` + `brain_trace_steps` with `brain_version='v3'`; readable from existing tables
- **Cost calc** — v3 turns insert into `llm_usage` via existing `trackUsage` helper; visible in `GET /api/workspaces/{slug}/usage` filtered by `action_type='brain_v3'`
- **Streaming chat updates** — `brain.chunk` WS events fill in the message bubble token-by-token (limited by Anthropic's `agent.message`-block-level SSE, not true token streaming, but visibly progressive on tool-using turns)

### Phase 3 — Skills

- **Pre-built (Anthropic-published, free)**: `docx`, `xlsx`, `pdf`, `pptx`
- **Custom upload pipeline** in `internal/brain3/skills.go`:
  - SKILL.md content authored as Go string constants
  - `Beta.Skills.New` for first upload, `Beta.Skills.Versions.New` for content updates (auto-detected via SHA-256 of SKILL.md)
  - Per-workspace cache: `mga_skill_<name>_id`, `mga_skill_<name>_hash`
  - Tool catalog hash includes per-skill content hash → SKILL.md edits auto-fire tools-drift on existing agents
- **Custom skills shipped (5 + 2 personas):**
  - **`decision-log`** — captures decisions to `/decisions/<date>-<slug>.md` in memory_store; **also dual-writes a `brain_memories` row** with `source='v3'` + `type='decision'` so the existing memory panel UI shows the decision
  - **`task-conventions`** — encodes always-ask-first, `todo` default status, structured proposal shape (Title / Status / Priority / Assignee / Acceptance), title format rules
  - **`writing-plans`** — multi-step requests get decomposed into `/projects/<date>-<slug>.md` plans + one task per step (replaces standalone `create_task`)
  - **`executing-plans`** — work through an existing plan step by step, with confirmation gates on side-effecting actions
  - **`verification-before-completion`** — never claim "done" without a verifying tool call; catches hallucinated success
- **Personas (custom skills, polymorphic — Brain takes them on for deliverable-shaped requests):**
  - **`creative-director`** — Ad Creative, Campaign Ideation, Brand Review workflows. Output contract: `[skill:Workflow]` badge + structured Ad Breakdown / Visual Strategy / Next Steps + image + collapsible Image-prompt panel
  - **`researcher`** — Quick Research, Comparison Brief, Source-Cited Memo, Social Pulse workflows. Cited findings, bottom-line first. Harnesses `nexus_web_search`, `fetch_url`, `search_x` (Grok), and the workspace's existing Social Pulse data via `list_social_pulses` + `get_social_pulse`

### Phase 4 — Personas + image gen + UX (2026-05-06)

- **V3OperatingGuide rewrite** — Personas section is now load-bearing in the always-loaded system prompt. Ad Creative + Quick Research output templates inlined (progressive-disclosure for skill bodies turned out unreliable); other workflows defer to skill bodies
- **Image generation** — `generate_image` tool defaults to Google's Nano Banana 2 (`gemini-3.1-flash-image-preview`); `aspect_ratio` enum added; v3 dispatch wraps the result so Claude embeds the markdown URL verbatim
- **Two new tools** for Researcher: `list_social_pulses` (read-only, fast) + `get_social_pulse` (full structured data) — exposes the workspace's existing Grok-backed Social Pulse pipeline (sentiment + themes + key posts + predictions + recommendations) to Brain without new provider plumbing
- **Auto-invalidate sessions on drift** — when any drift detector fires, `EnsureProvisioned` clears `brain_managed_sessions` so the in-flight turn picks up the new agent version. Replaces "user must click Reset Agent" friction
- **Per-conversation busy lock** — Brain v3 mimics human turn-taking. One reply per (slug, channel_id, parent_id) at a time; parallel across conversations. New mention in a busy conversation drops silently (the per-channel agent indicator already signals Brain is working). Keyed map on Server struct (`brainV3Busy` + `brainV3BusyMu`); global `agentSem` stays as a workspace-wide rate cap
- **Live progress messaging** — `PipelineConfig.OnToolStart` / `OnToolEnd` callbacks fire around custom_tool_use / agent.tool_use / mcp_tool_use events. The v3 server handler bridges them to `broadcastAgentState('tool_executing', humanLabel)` — same channel v1/v2 use. Tool names are humanized via `v3HumanToolLabel` map: `generate_image` → "the image generator", `nexus_web_search` → "web search", `list_social_pulses` → "the social pulse history", etc. Chat reads "Brain is using web search..."
- **Legacy gate** — `@Creative Director` + `@Caly` hidden from member list / agent picker in v3 workspaces. `BuiltinAgent.Legacy bool` + `brain.LegacyAgentIDs/MemberIDs` helpers; server-side `shouldHideLegacyAgents(slug)` check filters both `handleListAgents` and `handleGetWorkspace`. Toggleable via `show_legacy_agents=true`. Data preserved at all layers — switching `brain_version` back to v1/v2 restores them

### Drift detection (auto-update of existing agents)

The agent's config is captured at create-time, so changing settings would normally require manual reset. v3 detects three kinds of drift on every turn's fast-path:

| Drift type | Trigger | What happens |
|---|---|---|
| **Model** | `mga_model` differs from `mga_provisioned_model` | `Beta.Agents.Update` with new Model; bump version |
| **System prompt template** | `mga_system_prompt_template` differs from `mga_provisioned_template` | `Beta.Agents.Update` with new System (re-resolved via `ResolveSystemPrompt`); bump version |
| **Tools / skills catalog** | `ToolCatalogHash(tools)` differs from `mga_provisioned_tools_hash` (hash includes tool list + AgentToolsetRevision + per-skill content hashes) | `Beta.Agents.Update` with new Tools + System + Skills together; uploads any new SKILL.md content versions in the same pass |

Each drift fires at most once per change; subsequent turns are no-ops. Adds ~200–500ms to one message after a setting change.

### v1/v2 silo

| File | Edits |
|---|---|
| `internal/brain/` (v1) | **0 edits** |
| `internal/brain2/` (v2) | **0 edits** |
| Existing agent system | **0 edits** |
| `ws.go` | one switch added (default = v1) |
| `brain.go` | additive (allowlist keys, sensitive-key flag) |
| `migrations.go` | additive (3 new migrations) |
| `brain_memories` | new `source='v3'` value alongside existing `pin`/`llm` |

---

## Architecture in one paragraph

Each Nexus workspace (with v3 enabled) owns a single Anthropic Agent + Environment + memory_store, lazy-created on first use. Brain v3 is per-(channel, parent_id) sessions referencing that agent. Every turn, the v3 server handler builds the system prompt (workspace SOUL.md + INSTRUCTIONS.md + the v3 Operating Guide + a memory addendum), pre-injects pinned context + the sender's profile into the user message, opens an SSE stream, sends the user message, consumes events (`agent.message` for text → broadcast as streaming chunks; `agent.custom_tool_use` for Nexus tools → dispatch via `s.executeTool`; `agent.tool_use` for built-in file tools → counted in trace), and writes a final brain message + trace + cost record when the session goes idle. Decision-flavored writes to `/decisions/*.md` are dual-written to `brain_memories` for UI parity.

---

### Phase 5 — Product framing + UX consolidation (2026-05-07)

A focused day reorganizing Brain Settings around two engines instead of `v1/v2/v3` internal jargon, then unifying the surfaces so config has one source of truth per engine.

**Engine restructure (2 engines now):**
- `engine` brain_setting (`openrouter` | `claude`) — user-facing primary choice. `brain_version` + `ollama_enabled` derived as side-effects when engine is saved.
- `resolveEngine(slug)` server helper; ws.go routing switches on engine instead of brain_version.
- Two big engine cards at top of Settings; selected card expands inline with API key + model + advanced.
- Local AI components hidden behind `SHOW_LOCAL_AI=false` flag (kept in code for the future Pro/enterprise tier).
- Default for new workspaces: `openrouter`.

**Live OpenRouter model browser** (`internal/server/models.go`):
- `is_moe` detection on patterns (DeepSeek V4, Mixtral, Qwen3 a22b, DBRX, Jamba, Grok 2)
- Cache window 1h → 5m so newly-launched models surface fast
- `POST /api/workspaces/{slug}/models/test` — one-shot completion with latency + tokens
- Modal filters: Free / New / MoE / Tools / Vision; per-row Test button with inline result panel
- Curated quick-pick dropdown in OpenRouter engine card: DeepSeek V4 Pro, V4 Flash, Gemma 4, Qwen3 235B, Mixtral 8x22B, Llama 3.3

**Bug fix: xAI was hijacking model choice** (huge silent bug). `makeBrainClient` had a legacy override that routed every brain call through `xai_model` whenever an xAI key was present (auto-enable branch fired even when `xai_enabled` was empty). Removed. Brain now respects the user's model selection. Grok stays a *service* (search_x, social pulse, Researcher persona's Social Pulse workflow).

**Runtime ground truth in prompt** — every pipeline (v1/v2/v3) injects a "Runtime" block naming the actual model in effect for that turn, so the LLM doesn't parrot earlier turns when models switch.

**Auto-CAPABILITIES section** — pure code-gen `Server.BuildCapabilitiesSection(slug, engine, model)`. Reads engine + service keys + attached MCP, returns markdown describing what Brain has *right now*. Wired into all three pipelines (system prompt for v1/v2; PreloadedContext for v3 via per-turn `<context>` block). Brain answers "what can you do here?" with current truth instead of training-data guesses.

**Settings tab top summary chip row** — active engine + abbreviated model + service chips (Images / Grok / Web / OpenAI) + skills/MCP counts + "View Extensions →" button.

**Tools + Skills → "Extensions" tab** — single nav item replaces the two separate tabs. Inside: engine matrix (Claude vs OpenRouter side-by-side), workspace skills summary, MCP integrations summary. Legacy Skills + Tools tabs reachable via "Manage →" buttons within Extensions.

**MCP catalog curated to 4** — only GitHub, Notion, Slack, Google Drive offered as new options. 14 redundant/niche templates dropped from discovery (existing connected servers untouched).

**Engine Keys section unified into engine cards** — one source of truth per engine. Free Auto router moved into OpenRouter card's Advanced disclosure; system prompt template + provisioning info + skills attached + Reset Agent moved into Claude card's Advanced disclosure. Old Engine Keys section gated behind `{#if false}`.

**Manual triggers in Automations section:**
- "Run reflection now" → POST /brain/reflect (skips automations gate)
- "Extract memories now" → scoped to active channel
- Both use the active model (no more silent GPT-4o-mini fallback): `memory_model` default changed from `openai/gpt-4o-mini` to empty so `memoryComplete` falls back to brain `model`

**Reflection date hallucination fix** — the prompt template had a literal `{current date}` placeholder that was never substituted, so the LLM filled it with a training-cutoff guess (October 4, 2023). Now: actual date string injected, system prompt names today's date, explicit "do not invent dates from training data" instruction.

**v3 .md edits propagate live** — `handleUpdateBrainDefinition` clears `mga_provisioned_tools_hash` when engine is Claude. Next turn fires drift detection → re-resolves system prompt → auto-invalidate clears in-flight session → new prompt applies. No more Reset Agent click for SOUL/INSTRUCTIONS/etc. edits.

**Slash menu engine context header** — typing `/` in any channel shows the active engine + abbreviated capability summary at the top of the slash command popup.

## Test verification

| Path | Verified live? | How |
|---|---|---|
| Agent + env + memory_store provisioning | ✅ | Test workspace `u0nsas` provisions on demand |
| Per-channel-thread sessions | ✅ | Multiple channels each get a distinct session |
| Custom tool dispatch | ✅ | `fetch_url` fired correctly on a "btc price" turn (CoinGecko) |
| Streaming | ✅ | `brain.chunk` events flow; visible token streaming on multi-message turns |
| Memory writes (file tools) | ✅ | `/people/*.md` seeded; `/decisions/2026-05-05-ship-phase-1-3.md` written end-to-end |
| Decision dual-write to brain_memories | ✅ | `brain_memories` row with `source='v3'`, `type='decision'`, summary `"Ship Phase 1.3"` confirmed |
| `task-conventions` (always-ask-first) | ✅ | Live test on `u0nsas` showed scoping questions before task creation |
| Drift detection (model + template + tools) | ✅ | Agent version observed bumping 3 → 5 → 6+ as settings + catalog changed; no manual resets needed |
| **Auto-invalidate sessions on drift** | ✅ | Persona changes propagate to existing chats without manual Reset (verified after r6 → r7) |
| **Image generation (Nano Banana 2)** | ✅ | Banner with literal text rendered correctly; markdown URL embedded; collapsible Image-prompt panel rendered |
| **Researcher persona** | ✅ | "Research Claude managed agents pricing" produced cited findings with `[skill:Quick Research]` badge (took ~30s, multiple web searches) |
| **Live progress messaging** | ✅ | `Brain is using web search...` / `the image generator...` indicators visible during research turn |
| **Per-conversation busy lock** | ✅ | Same-conversation double-tap drops silently; cross-channel parallelism preserved |
| Cost calc + `/usage` integration | ⚠️ Code shipped, dashboard query not yet eyeballed |
| Memory viewer panel | ⚠️ Code shipped, frontend render not yet eyeballed |
| Creative Director persona | ⚠️ Image gen verified; full Ad Breakdown shape pending verification after r7 drift |
| Legacy gate (CD + Caly hidden) | ⚠️ Code shipped, frontend filter not yet eyeballed |

---

## Recommended cut

**Done.** Tagged `v3.0.0-beta.1` at commit `bef62ef`; [PR #1](https://github.com/vortex-303/nexus/pull/1) open from `brain-v3-claude-managed-agents` → `main` with the full feature summary.

**Pre-merge checklist:**

- [x] Personas verified end-to-end (Researcher: cited findings; Image gen: image + URL embedded)
- [x] Per-conversation lock verified (double-tap dropped, cross-channel parallel)
- [ ] Creative Director Ad Breakdown shape verified after r7 drift propagates
- [ ] Memory viewer panel verified (one screenshot away)
- [ ] Cost dashboard shows v3 entries (one query away)
- [ ] Legacy gate verified — CD + Caly absent from member list / mention picker in v3 workspace

**After merge:** default `brain_version` stays `v1` for all workspaces. Owners opt in per-workspace via Brain Settings → Brain Pipeline → v3 (BETA). No migration, no surprise upgrades.

## Backlog after beta.1

### Polish queue (high-leverage, small)

- Static welcome enhancement (Layer 1 — richer template with workspace data, no LLM, token-free)
- Brain-created task UI badge (~10 LoC, data is already in `tasks.created_by`)
- Spend cap + auto-disable
- Reconnect/dedupe for dropped streams (production hardening)
- Spanish-LATAM polish skills: `es-tone`, `bilingual-en-es`, `latam-business-norms`

### Feature additions

- `create_event` brain tool (calendar table exists, no tool surface)
- `create_recurring_task` / scheduler exposure (task scheduler exists; `create_task` doesn't expose `scheduled_at`)
- `image-brief` skill — turn vague visual asks into structured briefs (extends Creative Director persona)
- Refresh members seed when new members join (one-shot today)
- Sessions admin panel (debug)
- Observatory UI (no UI panel exists yet — only the backing tables are populated)

### Long-term

- `skill-distiller` meta-skill that proposes new SKILL.md drafts from observed patterns
- Promote v1/v2 `.md` skills → v3 Anthropic Skills via UI
- Two-way `members.title/bio` ↔ `/people/<slug>.md` sync
