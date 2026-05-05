# Brain v3 — Status

> **Snapshot: 2026-05-05.** Branch: `brain-v3-claude-managed-agents`.
> Live at `nexusteams.dev` (test workspace `u0nsas` only — `brain_version='v3'`).

## Headline

Brain v3 — Claude Managed Agents-backed alternative to v1/v2 — is **end-to-end working in production**. A workspace owner toggles `brain_version='v3'`, configures an Anthropic API key, and gets:

- Claude Sonnet 4.6 (default), Haiku 4.5, Opus 4.7, or Opus 4.6 as the brain model
- Per-channel-thread persistent sessions (Anthropic-hosted)
- Workspace-scoped memory_store with 5 file types Claude reads/writes (`read`, `write`, `edit`, `glob`, `grep`)
- 6 skills attached: 4 Anthropic pre-built (docx, xlsx, pdf, pptx) + 2 custom (decision-log, task-conventions)
- Visible memory viewer panel showing what Claude has learned
- Cost + traces integrated with the existing `/usage` dashboard

v1 and v2 are completely untouched — opt-in per workspace, default off.

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
- **Custom skills shipped**:
  - **`decision-log`** — captures decisions to `/decisions/<date>-<slug>.md` in memory_store; **also dual-writes a `brain_memories` row** with `source='v3'` + `type='decision'` so the existing memory panel UI shows the decision
  - **`task-conventions`** — encodes always-ask-first, `todo` default status, structured proposal shape (Title / Status / Priority / Assignee / Acceptance), title format rules

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

## Pending — sorted by leverage

### High value, in flight

- **`writing-plans` skill** — turn vague multi-step requests into structured plans saved to `/projects/<slug>.md`, with one Brain-created task per step. **In design (Q B–F open).**
- **`executing-plans` skill** — work through an existing plan step by step. Pairs with `writing-plans`.
- **`verification-before-completion` skill** — verify before claiming "done" (call `list_tasks` to confirm task created, `read` the doc, etc.). The discipline skill that prevents hallucinated success.

### Phase B remaining

- `meeting-summary` — chat thread → Decisions / Action Items (auto-`create_task`) / Open Questions
- `onboarding-playbook` (Layer 2 LLM walkthrough) — pairs with the not-yet-shipped Layer 1 enhancement (richer static templated welcome with workspace data)
- `bug-triage`, `knowledge-curation` (gated), `handoff-note`
- `weekly-digest` (gated on `automations_enabled` — needs scheduler hookup)
- Spanish-LATAM polish: `es-tone`, `bilingual-en-es`, `latam-business-norms`
- Fork `obra/superpowers`: writing-plans / executing-plans / verification-before-completion (per the planning trio above; we're authoring fresh rather than literal-forking)

### Operational + UI

- Static welcome enhancement (Layer 1 — richer template with workspace data, no LLM)
- Brain-created task UI distinction (~10 LoC, data is already there via `tasks.created_by = BrainMemberID`)
- Spend cap + auto-disable
- Reconnect/dedupe for dropped streams (production hardening)
- Sessions admin panel (debug)
- `automations_enabled` gate wiring (only relevant when first auto-firing skill ships)
- Refresh members seed when new members join (one-shot today)
- Observatory UI (no UI panel exists yet — only the backing tables are populated)
- `create_event` brain tool (calendar events have full DB + scheduler support, no brain tool surface)
- `create_recurring_task` / scheduling exposure (task scheduler exists; `create_task` doesn't expose it)

### Phase C / long-term

- `skill-distiller` — meta-skill that proposes new SKILL.md drafts from observed patterns (the long-tail compounding play)
- Promote v1/v2 `.md` skills → v3 Anthropic Skills via UI
- Two-way `members.title/bio` ↔ `/people/<slug>.md` sync

---

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
| Cost calc + `/usage` integration | ⚠️ Code shipped, dashboard query not yet eyeballed |
| Memory viewer panel | ⚠️ Code shipped, frontend render not yet eyeballed |

---

## Recommended cut

The current branch state is a defensible **Brain v3 beta** release point. What that should look like:

1. **Tag** `brain-v3-claude-managed-agents` at HEAD as `v3.0.0-beta.1` (or whatever the project versioning convention is)
2. **Open a PR to `main`** with this status doc as the description summary
3. **Don't merge yet** — at least until:
   - You've verified the memory viewer renders correctly in the UI (one screenshot away)
   - Cost dashboard shows v3 entries (one query away)
   - Optional: ship the planning skills so the v3 Beta has a workflow story (writing-plans + executing-plans + verification-before-completion)
4. **After merge to main**, default `brain_version` stays `v1` for all workspaces. Owners opt in per workspace via Brain Settings — no migration, no surprise upgrades.

If the planning skills land before the beta cut, this becomes a more complete release: v3 has memory + decisions + tasks-with-conventions + plans-with-execution. That's a coherent "agent that coordinates work" story.

If you want to cut now and add planning later, the current state is already a meaningful product surface — just label it more conservatively (e.g. v3 BETA — "captures decisions, tasks with conventions, persistent per-thread memory").
