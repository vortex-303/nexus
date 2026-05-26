# Personas v2 — Plan

> Multi-phase plan to turn Personas from hardcoded constants into a first-class,
> user-creatable workspace entity with explicit `/persona` invocation, visible
> activation badges, per-persona skill bundles, and per-persona model override.
>
> Saved 2026-05-26. Decisions locked below; see `## Decisions taken`.

## Goal

Today's personas (Creative Director, Researcher) are:
- Hardcoded in `internal/brain/persona_skills.go` and `internal/brain3/skills.go`
- Activated by substring keyword match (`MatchSkillsByContent`) with known false-positive bugs (e.g. `"ad"` matches `"add"`)
- Not customizable per workspace
- Not selectable by the user explicitly

We want:
- **Persona = first-class workspace entity** (DB row, full CRUD, per-workspace customization)
- **Explicit invocation** via `/<persona-slug>` slash command in the chat input
- **Visible badge** on Brain's reply: "Brain is now active as <persona>"
- **Per-persona model** (OpenRouter model override; falls back to workspace default)
- **Per-persona skill bundle** (subset of workspace skills available when persona active)
- **User-creatable personas** with AI-generate-from-prompt option (Phase 2)
- **Skills research phase** in parallel: fix activation mechanics, dedupe, add multi-language

## Decisions taken (2026-05-26)

| Question | Answer |
|---|---|
| Where to store personas? | **Workspace DB** (per-workspace `personas` table) |
| Phase 1 scope? | **Minimal**: read-only DB + slash + badge (~1 day + 3–4h for v3 split). No new UI. |
| Keep keyword matching as backward compat? | **Yes + fix substring bugs** (`"ad"`, English-only, ambiguous multi-match) |
| Skill research timing? | **In parallel** with Phase 1 (doc-only, no code) |
| Activation model when `/persona` is used? | **Clean swap** — persona body REPLACES the polymorphic meta-rules section instead of being inlined on top. Polymorphic stays alive only for the no-slash backward-compat path. |

### Why clean swap (not polymorphic-with-persona-inlined)

Today's polymorphic framing in `brain3/system_prompt.go:213-349` exists because there's no explicit invocation — Brain has to decide which persona applies, so the system prompt teaches it triggers + contracts for ALL personas, every turn. Costs ~1000 tokens/turn of meta-rules and creates ambiguity.

With `/persona`, the user makes the decision. Brain doesn't need to know about other personas, only the one currently active. Better mental model ("You ARE the Creative Director" vs "You are polymorphic, switch when..."), lower token cost, higher output-contract compliance, dramatically simpler to plug in user-created personas (their body just becomes the system prompt — no meta-rule editing needed).

**Hybrid path:**

```
/persona invoked          →  CLEAN SWAP: persona body + base, no polymorphic meta
keyword match (no slash)  →  TODAY'S PATH: polymorphic + persona inline (backward compat)
default voice (neither)   →  base only (unchanged)
```

Explicit slash always wins over keyword match. Persona persists for the rest of that `@Brain` mention chain; new mention chain resets to default.

## Data model

```sql
CREATE TABLE personas (
  slug              TEXT PRIMARY KEY,    -- "creative-director", "researcher", custom
  display_name      TEXT NOT NULL,
  description       TEXT NOT NULL DEFAULT '',
  body              TEXT NOT NULL DEFAULT '',   -- canonical persona body
  body_openrouter   TEXT NOT NULL DEFAULT '',   -- optional terse variant
  model             TEXT NOT NULL DEFAULT '',   -- "" = workspace default
  skills            TEXT NOT NULL DEFAULT '',   -- CSV of skill file slugs
  autonomy          TEXT NOT NULL DEFAULT 'reactive',  -- reactive | proactive
  trigger_mode      TEXT NOT NULL DEFAULT 'both',      -- slash | keyword | both | llm
  keywords          TEXT NOT NULL DEFAULT '',          -- CSV; used when trigger_mode includes keyword
  avatar_url        TEXT NOT NULL DEFAULT '',
  builtin_locked    INTEGER NOT NULL DEFAULT 0,        -- 1 = can't delete (built-in)
  enabled           INTEGER NOT NULL DEFAULT 1,
  created_by        TEXT NOT NULL DEFAULT 'system',    -- 'system' or member id
  created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
```

Seeded built-ins on workspace open (idempotent): Creative Director, Researcher — pulled from existing constants in `persona_skills.go`. Built-ins are `builtin_locked=1`; user can edit but a "Reset to default" button restores from the constants.

## Phase 1 — Read-only DB + slash + badge + clean-swap (~1 day + 3–4h v3 split)

### Backend

1. **Workspace migration v62**: create `personas` table.
2. **Seed personas on workspace open**: in the same path that calls `SeedPersonaSkills` today (`brain.go:107`), additionally `INSERT OR IGNORE` the two built-ins from `brain.PersonaSkills`.
3. **Persona loader**: `internal/brain/personas.go` — `LoadPersonas(db)` returns `[]Persona`. `LoadPersonaBySlug(db, slug)` for single lookup.
4. **Read API**:
   - `GET /api/workspaces/{slug}/brain/personas` — list (replaces the eventual UI's data source)
   - `GET /api/workspaces/{slug}/brain/personas/{slug}` — detail
5. **Prefix parser** in `handleBrainMention` (`brain.go:191`):
   - Detect leading `[persona:<slug>]` after the `@Brain` mention is stripped
   - On match: **clean swap** — load persona, replace polymorphic section with `"You are operating as <DisplayName>." + persona.body`. Switch model if `persona.model != ""`. Filter skills to `persona.skills` set if non-empty.
   - Re-emit the assistant message with a leading `[persona:<slug>]` line so the frontend renders the badge
6. **v3 system-prompt split** (`brain3/system_prompt.go`): factor the "Personas (LOAD-BEARING)" block (lines 213-349) into a function that returns either:
   - the polymorphic meta + inline templates (default, no persona invoked) — **current behavior unchanged**
   - the persona body verbatim (when invoked via `/persona`) — clean swap
7. **v1/v2 system-prompt split** (`brain_tools.go:909, :964`): conditional — if `[persona:X]` was parsed, REPLACE the persona-context slot with `body_openrouter` as the operating-mode directive (not the "# Active skills" list). If just keyword match, today's behavior.
8. **Backward compat**: keep current keyword-matching code in `MatchSkillsByContent`, but switch the source from hardcoded constants to DB rows where `trigger_mode IN ('keyword', 'both')`. Fix the word-boundary bug at the same time (use regex `\b<keyword>\b` instead of `strings.Contains`).

### Frontend

7. **Extend `slashCommands` derived list** (`+page.svelte:1023`):
   - Fetch `/api/workspaces/{slug}/brain/personas` once on workspace load
   - For each enabled persona with `trigger_mode IN ('slash','both')`: add `{ name: persona.slug, description: persona.description, action: '@Brain [persona:' + persona.slug + '] ', category: 'Personas' }`
8. **Badge rendering** — extend `extractSkillBadge` (`+page.svelte:3869`) to also handle `[persona:X]`:
   - Look up persona by slug from the loaded list
   - Render with distinct styling (avatar + "Active as: Creative Director" label) — different color than `[skill:X]` badges
9. **Slash menu styling for personas category** — already has category grouping; just need a Persona icon.

### Tests / verification

- Local: `make dev`, send `/creativedirector make a 16:9 banner for X`, expect:
  - Slash menu shows "Personas" category
  - Input becomes `@Brain [persona:creative-director] make a 16:9 banner for X`
  - Brain reply leads with persona badge
  - Output follows Creative Director template
- Keyword path: send `@Brain make a banner` (no slash) — keyword `banner` still matches Creative Director (`trigger_mode='both'`)
- Keyword bug fix: send `@Brain add a comment` — `"ad"` no longer false-fires Creative Director

## Phase 2 — Persona Creator UI (~2–3 days)

### Workspace Settings → Personas tab

- List view: built-ins first (locked, blue chip), then custom. Toggle on/off per persona.
- **"+ New Persona"** modal with two tabs:
  - **From scratch**: form with all Persona fields
  - **Generate from prompt**: natural-language ask → AI generates draft (reuses `skill_distiller.go` pattern, new `persona_distiller.go`)
- **Edit form**:
  - Display name, slug (auto from name, editable for new only)
  - Description (1-line)
  - System prompt body (monospace textarea, ~250 words target)
  - OpenRouter-terse variant (collapsible, optional)
  - **Model dropdown** — populated from `/api/workspaces/{slug}/models` + "Use workspace default"
  - **Skill multi-select** — checkboxes of all workspace skills, with bundled ones checked
  - Autonomy (radio: reactive / proactive)
  - Trigger mode (radio: slash only / keyword only / both / llm-routed)
  - Keywords (chip input, shown only when trigger mode includes keyword)
  - Avatar URL
- **Test panel**: side-by-side preview channel ("Try in #brain-testing"), shows persona response with badge
- **Reset to default** for built-ins (POST endpoint reverts row to constants)
- **Edit history** (reuses existing audit log; show last 5 changes per persona)

### Backend additions for Phase 2

- `POST /api/workspaces/{slug}/brain/personas` (create, role-gated to admin or PermSkillManage)
- `PUT /api/workspaces/{slug}/brain/personas/{slug}` (update)
- `DELETE /api/workspaces/{slug}/brain/personas/{slug}` (delete, fails on `builtin_locked=1`)
- `PUT /api/workspaces/{slug}/brain/personas/{slug}/reset` (built-ins only)
- `POST /api/workspaces/{slug}/brain/personas/generate` — AI-distill from prompt
- `PUT /api/workspaces/{slug}/brain/personas/{slug}/toggle` (enable/disable)

## Phase 3 — Skill research + polish (parallel, doc-only first)

### Research questions

1. **Activation mechanics drift between v1/v2 (substring keywords) and v3 (LLM judgment).** Can we converge on LLM-routed activation for v1/v2 (one cheap classifier call before the main turn)? Cost/latency vs. accuracy tradeoff.
2. **Skill catalog audit** — what's redundant across `caly/`, `creative_director/`, `shared/`? Specific suspects:
   - `caly/research-brief.md` vs Researcher persona's "Quick Research" / "Source-Cited Memo" workflows
   - `shared/summarizer.md` vs `shared/daily-review.md` overlap
3. **Language support** — every shipped skill is English-only. Should we:
   - Add `lang` field per skill + translate existing → `-es` slug suffix? OR
   - Have skills be language-agnostic and let the LLM produce in user's language? (Probably the latter, but need to test)
4. **Output contract enforcement** — `"MUST follow this template"` works ~80–90%. Stronger options:
   - Structured outputs (JSON schema) for persona replies, render template on frontend
   - LLM grader: post-process every persona reply and re-prompt if contract violated
5. **Skill marketplace** — browse/install community skills inside Nexus. Sources to consider:
   - Official Anthropic Skills repo
   - Curated set we publish ourselves (positions Nexus as a community-driven AI workspace)
   - Public user-contributed (defer; needs moderation)

### Skill research deliverables

- `RESEARCH_SKILLS.md` (separate doc) summarizing findings + recommendations
- Specific PRs for fixes (substring → word-boundary; dedupe duplicates; add `lang` field if we go that way)
- Catalog of "starter skill bundles" for common workspace types (Engineering, Marketing, Real Estate, Healthcare — aligned with Atlas verticals)

## Risks

- **Schema migration on a live product.** Test on a single workspace before deploying.
- **Model/engine mismatch** — persona wants `claude-sonnet-4-6` but workspace on v2 (OpenRouter). Don't auto-switch engines; show a warning ("This persona requires Brain v3").
- **Cost blowup** — a persona using GPT-4-class for every reply could blow token budget. Surface estimated cost in Persona Creator + per-workspace cap.
- **Skill bundle vs catch-all tension** — persona with narrow `skills` set may miss useful skills the user expected. Mitigation: optional "fall through to workspace skills if no bundle match" toggle.
- **v2/v3 parity** — some persona features (tight output contracts) work better on Claude. Document which capabilities are engine-conditional.

## Non-goals (explicitly out of scope)

- Persona-to-persona handoff mid-conversation
- Multi-persona panels ("ask all 3 at once")
- Persona-specific tool catalogs (overlap with skill bundle)
- Persona memory / own knowledge base — separate project
- Cross-workspace persona marketplace — defer to post-launch
- Persona auth permissions per role beyond enable/disable — over-engineering for now

## Status tracker

- [x] Audit + plan (this doc)
- [x] Design decision: clean swap on /persona, polymorphic stays for keyword path
- [ ] Phase 1: workspace migration v62 (personas table)
- [ ] Phase 1: seed built-ins on workspace open
- [ ] Phase 1: persona loader + read API endpoints
- [ ] Phase 1: prefix parser in handleBrainMention
- [ ] Phase 1: v3 system-prompt split (persona-mode vs polymorphic-mode)
- [ ] Phase 1: v1/v2 prompt-assembly conditional (clean swap vs polymorphic)
- [ ] Phase 1: keyword bug fix (substring → word boundary)
- [ ] Phase 1: frontend slash menu + persona category
- [ ] Phase 1: frontend badge rendering for [persona:X]
- [ ] Phase 1: local verify (slash + badge + keyword backward compat + bug fix)
- [ ] Phase 1: deploy + verify on prod
- [ ] Phase 2: Persona Creator UI
- [ ] Phase 3: Skill research doc → fixes
