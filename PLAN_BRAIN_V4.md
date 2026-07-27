# Brain v4 — One Brain, Model-Agnostic

> Written 2026-07-27, grounded in a full code map of HEAD (`brain-v3-claude-managed-agents`,
> `b5089fd`) and the last pre-retirement commit `82e1c94` (v3 source of record).
> Companion docs: `BRAIN_V3_STATUS.md` (what v3 proved), `BRAIN_2_REPORT.md`.

## Context — where we actually are

Commit `480af29` (2026-05-27, "Retire Brain v1 + v3: OpenRouter is the only engine")
already did the big consolidation: `internal/brain3/` deleted (4,648 lines),
`resolveEngine` hardcoded to `"openrouter"`, migration 63 forces `brain_version='v2'`
and scrubs `mga_*` keys. The Brain is already model-agnostic (OpenRouter + Gemini +
Ollama + Grok via `makeBrainClient`, `internal/server/brain.go:819`).

**v4 = finish the job.** Three workstreams:

1. **Close the v1 side doors** — v1's fixed 2-round loop still handles thread
   auto-follow, webhook/email/telegram ingest, and calendar triggers.
2. **Lift the proven v3 features** that are portable (busy lock, live progress labels,
   token streaming, file-based memory, traces) from `82e1c94`.
3. **Fix the real bugs** the code map surfaced (pinned memories never load, v2 reports
   zero tokens, flat 2048 max_tokens, reflector mostly disabled).

No new `internal/brain4/` package. v4 is `internal/brain2/` + shared `internal/brain/`
evolved in place; `brain_version` becomes `'v4'` cosmetically at the end.

---

## Phase 1 — One loop: retire v1 for real (S)

v1's loop (`internal/server/brain_tools.go:610` `handleBrainMentionWithToolsEx`) is still
reached from:
- `ws.go:476` — Brain thread auto-follow
- `ingest.go:67,73,83` — webhooks, inbound email, Telegram
- `calendar_triggers.go:212`

**Port v1's three wins into v2 first, then swap those call sites to `handleBrainV2`:**

- [ ] **`ResultAsAnswer` short-circuit** (v1 `brain_tools.go:833-848`): exactly 1 tool call
      whose `ToolDef.Function.ResultAsAnswer` is true → return raw tool result, skip the
      synthesizer round. This removes the root cause of v2's image-markdown-dropping
      hacks for `generate_image` / `web_search` / `fetch_url` / `create_document` / `search_x`.
- [ ] **Pre-LLM bypasses** (v1 `brain_tools.go:646-670`): `/search` + natural-language
      web-search regex → direct `toolWebSearch`; `/localsearch` → `toolSearchMessages`.
      Zero-LLM, zero-cost paths worth keeping.
- [ ] **`[persona:slug]` prefix parsing** (v1 `brain_tools.go:702-726`): full parse +
      `LoadPersonaBySlug` + persona model override. v2 today only string-matches the
      prefix for the tool_choice heuristic (`brain2.go:27`).
- [ ] **Extract the duplicated preamble** (~100 lines each in `brain_tools.go:610-780` and
      `brain2.go:38-140`): semaphore acquire, boot-time/staleness check (use the named
      constants `maxBrainChannelAge`/`maxBrainThreadAge` from `agent_runtime.go:25-26`),
      thinking-state broadcast, prompt build, history fetch, image attach, model resolve
      → one `prepareBrainTurn()` helper.
- [ ] Swap the 5 call sites to v2; delete `handleBrainMentionWithToolsEx` and
      `handleBrainMention` (`brain.go:192`, already zero callers).

## Phase 2 — Lift v3's portable wins + wire the orphans (M)

All lifted from `git show 82e1c94:...` unless noted. The three uncommitted files in the
tree (`brain2/trace.go`, `brain2/skill_trigger.go`, `server/brain_traces.go`) are complete
and schema-ready (migrations 59/60 already shipped) — they just need wiring.

- [ ] **Per-conversation busy lock** — copy `brain3.go:63-83` verbatim into `handleBrainV2`.
      The `brainV3Busy map` + mutex already exist on `Server` (`server.go:56-64,105`) —
      rename to `brainBusy`. One reply per (channel, parent_id), parallel across
      conversations, silent drop on double-tap.
- [ ] **Human tool labels** — copy `v3HumanToolLabels` + `v3HumanToolLabel`
      (`brain3.go:634-665`) and route v2's `tool_executing` broadcast
      (`brain2.go:190`) through it: "Brain is using web search…" instead of raw names.
- [ ] **Token streaming** — the one place v4 beats v3: OpenRouter does true token SSE.
      `CompleteStream` (`openrouter.go:662`) is implemented, `brain.chunk` protocol
      (`hub/protocol.go:51,198`) and the frontend consumer (`+page.svelte:1567`) exist.
      Lift `createEmptyBrainMessage` / `broadcastBrainChunk` / `finalizeBrainMessage`
      (`brain3.go:284-371`) and stream the synthesizer/final round.
- [ ] **Traces** — commit the three orphan files; instantiate `TraceCollector` in the v2
      pipeline; route `handleListTraces`/`handleGetTrace` in `server.go`; flush per turn
      with `brain_version='v4'`. (Skill-trigger stays dormant until the distiller uses it.)
- [ ] **Bug fixes** found by the map:
  - Pinned memories: `handlePinMemory` (`brain_memory.go:549`) writes `source='pin'` but
    never `pinned=TRUE`, so `BuildPinnedMemoryContext` always returns "". Set the flag
    (+ one-time backfill `UPDATE brain_memories SET pinned=TRUE WHERE source='pin'`).
  - v2 cost row has zero tokens (`brain2.go:269`) — accumulate real usage from
    `CompleteWithTools` returns.
  - `max_tokens` flat 2048 (`openrouter.go:592,672`) — wire the already-written
    `brain2.MaxTokensForModel` (`models.go`).
  - Reflector gaps (`brain2.go:279-290`): pass a real `Client` (memory_model) and
    resolve `SenderID` so profile updates + self-reflection actually run.
- [ ] **Unify** the twice-declared client interface (`brainCompleter` ≡ `brain2.LLMClient`)
      and the duplicated 100k-char prompt cap.

## Phase 3 — File-based memory, self-managed (M-L)

v3's best idea, re-homed locally. No Anthropic memory_store: the store is a sandboxed
per-workspace dir `<dataDir>/workspaces/<slug>/brain/memory/`.

- [ ] **Five file tools** — `read_memory`, `write_memory`, `edit_memory`, `glob_memory`,
      `grep_memory` as normal Nexus tools in `brain_tools.go`, path-jailed to the memory
      dir (reject `..`, absolute paths outside root). This is the biggest net-new lift.
- [ ] **Layout convention** (port prose from `82e1c94:system_prompt.go:8-59`):
      `/pinned.md`, `/INDEX.md`, `/people/<slug>.md`, `/decisions/`, `/projects/`,
      `/feedback/`, `/self/`.
- [ ] **`<context>` pre-injection** — port `PreloadedContext.Render` (`memory.go:93-145`):
      prepend pinned.md + people/<sender>.md + runtime-model anchor to the user message
      (better cache behavior than v2's system-prompt appends). Local file reads replace
      the two Anthropic API calls.
- [ ] **Member profile seed** — port `SeedMemberProfiles` + `renderSeedProfile`
      (`people.go:41-151`); only the write target changes to a local file. One-shot flag.
- [ ] **Decision dual-write** — port `parseDecisionWrite` (`stream.go:440-473`) +
      `persistV3DecisionsToBrainMemories` (`brain3.go:557-627`): a `write_memory` into
      `/decisions/` also inserts a `brain_memories` row so the existing Memory panel sees it.
- [ ] **Memory viewer** — reuse the endpoint contract from `brain3.go:491-547`
      (`{mount_path, memories:[{path, content, size_bytes, updated_at}]}`, never-5xx) at
      `GET .../brain/memory/files`; frontend caller `api.ts:547` already exists.
- [ ] **Skills** — port the 5 non-persona SKILL.md bodies from `82e1c94:skills.go`
      (decision-log, task-conventions, writing-plans, executing-plans,
      verification-before-completion) as seeded local skills via the existing
      `internal/brain/skills.go` substrate (creative-director + researcher already ported).
      Keep the content-hash discipline for reseeding without clobbering user edits.

## Phase 4 — Sessions (optional, decide after 1-3) (M)

Today context = last 40 messages × 4000 chars rebuilt per turn + rolling channel
summaries. v3's persistent per-(channel, parent_id) sessions felt materially better in
threads. Local equivalent: reuse the `brain_managed_sessions` table (migration 58, still
present) as `brain_sessions` holding a compacted rolling transcript per conversation,
compacted with `CompressOldToolResults`-style summarization. Evaluate after Phase 3 —
the `<context>` injection + summaries may be enough.

## Phase 5 — Rename, prune, ship (S)

- [ ] `brain_version` → `'v4'` (migration; keep the setting for future engines),
      traces stamped `v4`, sidebar pill copy.
- [ ] Delete dead code: `brain2/planner.go`, `brain2/models.go` leftovers,
      `executePlan`/`executeStep`, DDG scrapers (`brain_tools.go:1802-1939`),
      `brain_zero.go` path decision, `PipelineResult.Items` (or consume it for
      streaming), `BRAIN_V3_ENABLED` flag + dead engine cards + dead `api.ts` callers
      (`:532,547` repointed or removed).
- [ ] Update `CLAUDE.md` (dev port is **8080**, not 3000) and `STATUS.md`; fold
      `BRAIN_V3_STATUS.md` into an archive note.
- [ ] Then resume `DISTRIBUTION_PLAN.md` (GHCR → Litestream → deploy buttons) on a
      merged main.

## Verification per phase

- `go build ./cmd/nexus/` + `cd web && npm run build` after every phase.
- Live: `make dev` (port 8080) → @Brain in channel + thread + DM; `/search` bypass;
  image request (Creative Director path, image renders); double-tap a mention
  (second drops); watch "Brain is using…" labels; token streaming visible;
  memory files appear under Brain Settings; decision shows in Memory panel;
  traces listed via new endpoint.
- No test suite exists — consider adding table-driven tests for the file-tool
  path jail and `ValidateToolCall` while in there.

## Open questions (small)

1. Phase 4 sessions: build or skip? (Recommend: decide after using Phase 3 for a week.)
2. `packages/nexus-bridge-mac/` (untracked Swift pkg) + `licensegen` binary — commit,
   gitignore, or delete? Unrelated to v4 but sitting in the tree.
3. PR #1 (brain-v3 branch → main): merge before starting v4 so work lands on main.
