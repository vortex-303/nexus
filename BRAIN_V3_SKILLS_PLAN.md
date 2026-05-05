# Brain v3 — Skills Plan

> Strategy document for Brain v3's skill system. Phase A (4 pre-built file-format skills) is shipped. This document covers Phase B and beyond.

## What "skills" actually are, in plain terms

A **skill** is a folder of instructions that Claude reads only when it's relevant — like a playbook on a shelf. The shelf is on Anthropic's side; Claude knows the title of every book by default but only opens one when the current task warrants it.

Why this matters compared to dumping everything in the system prompt:
- The system prompt is read on every turn, costs tokens every turn.
- A skill description is tiny (~50 tokens), the full content is loaded only on the turns that need it.
- We can give Brain 30+ specialized playbooks without slowing every reply down.

Two kinds of skills:
1. **Anthropic-published skills** — uploaded by Anthropic, free to use. We reference them by name (e.g. `xlsx`, `internal-comms`).
2. **Custom skills** — folders we author, upload to our org via the Skills API, then attach to Brain. This is where the Nexus-specific value lives.

---

## Where we are now (Phase A)

Already shipped:

| Skill | What it does |
|---|---|
| `xlsx` | Reads, edits, generates Excel files. |
| `docx` | Reads, edits, drafts Word documents. |
| `pdf` | Extracts text + tables from PDFs. |
| `pptx` | Reads or generates slide decks. |

These cover the file-format basics. Phase B is about **Brain's behavior**, not file formats.

---

## Phase B at a glance

| Sub-phase | What ships | Effort |
|---|---|---|
| **B-1** | 3 Anthropic skills (`internal-comms`, `doc-coauthoring`, `skill-creator`) + 1 custom (`decision-log`) | ~1 day |
| **B-2** | 3 custom P0 (`meeting-summary`, `weekly-digest`, `task-conventions`) | ~1 week |
| **B-3** | 4 custom P1 (`onboarding-playbook`, `bug-triage`, `knowledge-curation`, `handoff-note`) | ~1 week |
| **B-4** | Fork 3 from `obra/superpowers` (`writing-plans`, `executing-plans`, `verification-before-completion`) | ~3 days incl. audit |
| **B-5** | Spanish-LATAM polish (`es-tone`, `bilingual-en-es`, `latam-business-norms`) | ~2 days |

**Total Phase B: ~3 weeks for ~14 production skills.** Always-loaded metadata cost ≈ 1.4k tokens — negligible.

---

## Token-cost gating: workspace-owner opt-in

A skill *being attached* to the agent costs nothing — only the metadata description sits in context. But a skill that **fires automatically on a schedule** (weekly digests, scheduled briefs, periodic knowledge-curation passes, skill-distiller proposals) burns tokens whether the workspace asked for it or not.

We already have the right infrastructure: `brain_settings.automations_enabled` (default `false`). v2's async reflector already gates on it. v3 reuses it.

**The rule: any skill or behavior that fires automatically (no human trigger) is gated by `automations_enabled=true`. Manual invocation always works.**

| Skill / behavior | Manual trigger | Auto trigger |
|---|---|---|
| `internal-comms` (draft an announcement) | always works (you ask for it) | n/a — never auto |
| `meeting-summary` | always works (you ask for it) | n/a — never auto |
| `decision-log` (in-turn write during a chat) | always works (in-turn behavior, free) | n/a — not "scheduled" |
| `weekly-digest` (Friday 9am post) | always works (any-time `@Brain weekly digest`) | gated — only fires if automations_enabled |
| `knowledge-curation` (periodic dedupe pass) | always works (`@Brain clean up memory`) | gated — only fires on schedule if enabled |
| `onboarding-playbook` (LLM-guided walkthrough) | always works (new member's first @Brain) | gated for proactive nudges |
| Per-member profile updates | always works (in-turn writes) | n/a — not "scheduled" |
| `skill-distiller` (Phase C, proposes new skills) | always works (`@Brain propose new skills`) | gated — only fires on schedule if enabled |

**Default workspace state:** automations off. Workspace owner toggles **Brain Settings → Automations → Enable automated actions** to opt in. UI already exists. v3 skills that need scheduling check the setting before firing.

---

# Detailed exploration of priority items

## 1. `internal-comms` — Anthropic, drop-in

**What it is, in plain terms:**
A playbook for writing internal communications that don't suck — announcements, newsletters, FAQs, status updates, change notifications. It teaches Brain how to structure these so they're scannable, accurate, and don't bury the lede.

**What it does in practice:**
When you ask Brain "draft an announcement for the v3 launch", instead of getting a generic blob, Brain follows the structure baked into the skill: TL;DR at the top, what changed, who's affected, what action's needed, when, links to references. It also knows to match register to the audience (all-hands vs. dev-team vs. customer-facing).

**Why it matters for Nexus:**
Team workspaces produce announcements constantly — feature drops, policy changes, incident updates, weekly digests, post-meeting recaps. This is bread-and-butter coordination work. Without the skill, Brain produces inconsistent quality. With it, every announcement has the same structural DNA.

**Outcome when shipped:**
- Asking Brain for any "communicate this to the team" request produces well-structured output by default.
- The structure is consistent across workspaces, channels, and authors (whichever member triggered Brain).
- Pairs naturally with our future custom skills like `weekly-digest` and `meeting-summary`.

**Cost:** none (Anthropic pays for it).

---

## 2. `skill-creator` — Anthropic, scaffolds new skills

**What it is, in plain terms:**
A skill whose job is helping Brain *write more skills*. It knows the SKILL.md format, the YAML frontmatter rules, the progressive-disclosure conventions, and the "how to validate" patterns.

**What it does in practice:**
When you tell Brain "I want a skill for our weekly all-hands prep", instead of Brain hand-rolling a skill that may or may not match Anthropic's spec, it loads `skill-creator` and produces a properly-structured skill folder: SKILL.md with valid frontmatter, helper scripts if needed, a description that makes it discoverable. Then we publish that skill via the Skills API.

**Why it matters for Nexus:**
This is the **compounding leverage** play. Without `skill-creator`, every new skill needs a human to write the YAML, structure the prose, follow the progressive-disclosure pattern. With it, Brain can author its own skills based on observed patterns. We collaborate with Brain on skill creation rather than doing it manually.

It also pairs with the `skill-distiller` meta-skill we'd build in Phase C — that one watches conversation patterns and proposes new skills, but `skill-creator` is what actually drafts them.

**Outcome when shipped:**
- Authoring a new custom skill drops from "1 day of careful YAML + prose" to "describe what the skill should do, review Brain's draft, adjust, publish".
- Long-term: the skill catalog grows organically as Brain identifies patterns worth canonicalizing.

**Cost:** none (Anthropic publishes it).

---

## 3. `obra/superpowers` workflow loops — fork into our org

The `obra/superpowers` repo is the most battle-tested community collection. The three skills below are the ones that map directly to how Nexus tasks and docs already flow. We **fork** (don't blindly install) — copy into our org, audit the YAML, re-publish under `nexus-skills/`.

### 3a. `writing-plans`

**What it is, in plain terms:**
A playbook for breaking a vague request into a concrete, sequenced plan with checkpoints. Not "write code now", but "here's what we're doing, in this order, with these decision points along the way."

**What it does in practice:**
User: "Brain, we need to ship the new pricing page by Friday."
Without the skill: Brain might create a single task and move on.
With the skill: Brain walks through scope (pages, copy, design assets, dev work, QA, deploy), identifies dependencies, proposes a sequenced plan, asks about ambiguous bits before committing.

**Why it matters for Nexus:**
Most Nexus workspaces have feature work, project work, multi-step initiatives. The "ship by Friday" → "here's the 7-step plan" transformation is exactly what coordination tools should do. This is the planning half of the loop; `executing-plans` is the doing half.

**Outcome:**
- Multi-step requests produce structured plans saved to `/projects/<slug>.md` in memory_store.
- Brain can hand off plans to humans or other agents who pick up specific steps.

### 3b. `executing-plans`

**What it is, in plain terms:**
The other half of the planning loop: given an existing plan, work through it step by step, marking progress, surfacing blockers, asking when stuck instead of guessing.

**What it does in practice:**
User: "Brain, work on the pricing page plan we made yesterday."
Brain reads `/projects/pricing-page.md`, identifies what's done and what's next, picks up the next un-blocked step, executes it (creates tasks, drafts copy, writes a doc), updates the plan file with progress.

**Why it matters for Nexus:**
A plan that nobody executes is just a doc. This skill closes the loop — Brain becomes an actual participant in moving the plan forward, not just a planner. Combined with `writing-plans`, you get a proper plan→execute cycle.

**Outcome:**
- Long-horizon work actually gets advanced between human check-ins.
- Plans stay current because Brain updates them as it goes (not just at the start).

### 3c. `verification-before-completion`

**What it is, in plain terms:**
A discipline skill: before declaring something done, verify it. Run the test, check the output, re-read the doc, confirm the task ID exists, etc. Catches the "Brain confidently says it's done but it isn't" failure mode.

**What it does in practice:**
Brain: "I created the task and assigned it to Alice."
Without the skill: Brain says this and moves on.
With the skill: Brain calls `list_tasks` to confirm the task exists, checks Alice is the assignee, then reports.

For docs: Brain re-reads what it wrote and checks for missing sections before declaring complete. For code: runs the test before claiming the bug is fixed.

**Why it matters for Nexus:**
Hallucinated success ("yes I did that") is the single most damaging failure mode for a coordination AI. This skill makes verification a default reflex, not an afterthought.

**Outcome:**
- Sharp drop in "Brain said it did X but X didn't happen" reports.
- Trust score goes up — humans can rely on Brain's "done" signals.
- Works hand-in-hand with `executing-plans` (verify each step before marking it done).

---

## 4. Hermes patterns worth porting

Hermes (NousResearch) is a different agent framework, but two patterns are worth lifting and adapting into our skill system.

### 4a. Per-member profile files (cheap, big payoff)

**What it is, in plain terms:**
A file per workspace member that Brain reads and updates over time. Like an HR record, but for Brain's understanding of the person — what they work on, how they prefer to be communicated with, what they've been asking about lately.

**What it does in practice:**
When Nico talks to Brain in any channel, Brain reads `members/nico/profile.md` from the memory_store before responding. The profile says things like:
- Role: solo dev shipping multiple products
- Preferences: terse responses, no emojis, bullet points OK
- Active focus: Brain v3 (this week), Mailstorm DKIM (last week)
- Communication style: thinks through tradeoffs out loud, asks for plans before code

Brain tailors its response to that. After the turn, if Brain learned something new ("Nico is also working on Atlas"), it edits the profile in-place.

**Why it matters for Nexus:**
Multi-tenant team workspaces are full of people with different communication styles and priorities. Without this, every member gets the same Brain. With it, Brain adapts per member.

This already partly exists in our memory_store layout (`/people/<slug>.md`) but we haven't formalized *how* Brain uses these files. Phase B-3's `onboarding-playbook` and the `decision-log`/`meeting-summary` skills should both **read and write** these files as part of their default flow.

**Outcome:**
- A member who's been around for a month feels like Brain "knows them" — because it has notes.
- Communication style adapts automatically (terse for those who like terse, detailed for those who don't).
- Onboarding new members produces a profile from day-one signals.

**This is mostly free** — the memory_store layout already supports it; we just need to encode "always read members/<sender>/profile.md first" into the relevant skills.

### 4b. `skill-distiller` — procedural memory (Phase C, the long-tail bet)

**What it is, in plain terms:**
A meta-skill that watches Brain's conversation history for recurring successful patterns and proposes new SKILL.md drafts to canonicalize them. Like "I noticed you've answered the same kind of question 5 times this week — should we make a skill out of it?"

**What it does in practice:**
- Periodically (weekly or on-demand), Brain runs `skill-distiller`.
- It reads recent successful conversations from memory_store + brain_traces.
- It looks for recurring patterns: same kind of request → same kind of response.
- It drafts a new SKILL.md (using `skill-creator`) with the distilled pattern.
- A human reviews and approves before the skill goes live.

**Why it matters for Nexus:**
This is the compounding play. Each workspace develops its own conventions, vocabulary, and recurring needs. `skill-distiller` turns that organic pattern into formal skills automatically. The skill catalog grows without anyone manually authoring everything.

**Why it's Phase C, not Phase B:**
- Needs Phase B's foundation (decision-log, meeting-summary, profile files all writing to memory_store).
- Needs enough conversation history per workspace to find real patterns (premature on a fresh workspace).
- Needs a human-gated review flow we haven't built yet.

**Outcome (Phase C, when ready):**
- Workspaces accumulate workspace-specific skills over months without explicit authoring.
- Patterns that humans don't even notice ("we always answer this kind of question this way") become explicit and replicable.
- Brain becomes more useful in workspace N+1 because the skills authored in workspace N transfer.

---

## Two infrastructure gotchas (lock in early)

### 1. Skills don't sync across surfaces

A skill uploaded for the API isn't automatically available in claude.ai. If marketing wants to demo a Brain v3 skill on claude.ai, that's a separate upload. Build a one-command sync script when this becomes relevant.

### 2. API skills have NO network access

Inside a skill's bash environment: no internet, no package installs at runtime. This means anything fetch-like — meeting-summary pulling Zoom transcripts, weekly-digest pulling Linear — must call our existing Nexus tools (`fetch_url`, `nexus_web_search`, `list_tasks`, `search_workspace`) from the prose, NOT from the skill's bash.

Document this convention in `skill-creator`'s template so every future skill follows it.

---

## Custom skills inventory (P0–P3, for reference)

### P0 — change how the product feels (Phase B-1 + B-2)

- `decision-log` — Capture a decision (context, options, rationale, owner, date) into memory_store with stable IDs and surface it in `search_workspace`.
- `meeting-summary` — Turn a chat thread / pasted transcript into Decisions / Action Items (auto-`create_task`) / Open Questions / Notes.
- `weekly-digest` — Pull from tasks + decisions + recent docs, generate per-workspace Friday digest.
- `task-conventions` — Encodes priority/label/owner conventions so `create_task` calls are consistent across agents.

### P1 — incremental but real wins (Phase B-3)

- `onboarding-playbook` — see "Onboarding reconciliation" section below; merges with existing static welcome.
- `bug-triage` — Bug report → reproduce checklist, severity, owner, linked task.
- `knowledge-curation` — Periodic dedupe/merge/retire on memory_store; flags stale notes. Auto-fire gated on `automations_enabled`.
- `handoff-note` — End-of-shift summary for an async teammate or the next agent run.

#### Onboarding reconciliation

**Constraint: onboarding must work for workspaces that haven't set up an Anthropic API key yet.** A new member joining a fresh workspace is the chicken-and-egg case — making the welcome experience depend on LLM access means it breaks the moment it's most needed. So **default onboarding is fully token-free**; the LLM walkthrough is an explicit opt-in.

Nexus already has the foundation: `handleBrainWelcome` (in `internal/server/brain.go`) sends a hardcoded welcome DM. It's static — no LLM, no per-user adaptation, one canned message. v1/v2 don't go further. The new design extends this without adding a token-cost path on the default journey.

##### Three layers, increasing in cost — only the cheapest is default

**Layer 1 — Static templated welcome (default, $0, no API key needed).**
Improve the current canned DM with template substitution from workspace data we already have in the DB:
- Workspace name, member count, channel list (top 5 by recent activity)
- Pinned messages count and a hint to check the pinned panel
- Top docs / knowledge items by reference count
- Who-to-ask-for-what — derived from workspace member roles (we already store these)
- A list of `@Brain` capabilities the user can try

All produced by simple string templates over SQL queries. **No LLM call.** Posted instantly when the member joins. Works even if the workspace has no Anthropic key, no v3 enabled, no anything — it's a pure-template flow on top of structured workspace data.

**Layer 2 — On-demand LLM walkthrough (manual opt-in, costs tokens per ask).**
Available only if the workspace has `anthropic_api_key` configured AND `brain_version=v3`. Triggered by an explicit ask from the member: `@Brain walk me through this workspace`, `@Brain who works on what`, `@Brain what should I read first`. v3 loads the `onboarding-playbook` skill, reads memory_store (`/people/`, `/projects/`, `/pinned.md`), produces a tailored walkthrough.

The workspace pays for *that ask*, but only because the member explicitly opted in by asking. No invisible cost.

**Layer 3 — Proactive nudges (gated, scheduled).**
Day-3 / day-7 check-ins ("here are 3 docs people referenced this week", "you haven't introduced yourself yet") only fire if `automations_enabled=true`. Same gate as the other scheduled skills.

##### What a new member experiences

| Workspace state | What they see |
|---|---|
| Fresh workspace, no Anthropic key | Layer 1 only: instant, well-templated welcome with workspace-specific info. Zero LLM cost. |
| Workspace has key, v3 enabled, automations OFF | Layer 1 on join. Layer 2 if they ask Brain anything. No proactive nudges. |
| Workspace has key, v3 enabled, automations ON | Layer 1 on join. Layer 2 if they ask. Layer 3 proactive nudges on day 3+. |

##### Where the work lives

- Layer 1: extend `handleBrainWelcome` to pull workspace data into a richer templated message. Pure Go + SQL, no v3 dependency. ~150 LoC.
- Layer 2: the `onboarding-playbook` skill. Authored once, attached to v3 agents. Triggered by explicit member request, never auto.
- Layer 3: Layer 2 skill + a scheduler hook that posts a follow-up day-N message, gated on `automations_enabled`.

This way the *core* onboarding (Layer 1) is free, fast, reliable, and doesn't care what brain version or API key the workspace has. The richer experience (Layers 2 + 3) is opt-in by either the member (Layer 2) or the owner (Layer 3).

### P2 (later)

- `code-review-lite` — Review pasted diffs for obvious issues. Explicitly NOT a security review.
- `rfc-author` — Structured RFC/proposal authoring with our template.
- `standup-roundup` — Daily standup synthesis from chat + task deltas.

### P3 (someday)

- `incident-postmortem` — Blameless postmortem template.

### Spanish-LATAM polish (Phase B-5)

PT/Brazil dropped from scope — Nexus v3 targets Spanish-speaking LATAM only.

- `es-tone` — Spanish register guide. Covers rioplatense (Argentina/Uruguay) and neutral LATAM Spanish; tone choices (formal vs. informal `tú`/`vos`/`usted`), pronoun consistency, common-LATAM idioms vs. Spain-Spanish words to avoid.
- `bilingual-en-es` — Auto-detect EN vs. ES on input; respond in same. When the conversation mixes both (common in tech teams), provide inline ES↔EN gloss for ambiguous terms. Pairs with the babel project's glossary work.
- `latam-business-norms` — Spanish-LATAM only: Argentina/Mexico/Colombia/Chile/Peru holiday calendars, working-hours norms, formal/informal address per country, common business terms (CUIT/RFC/RUT etc.). No BR content.

### Vertical packs (deferred to Atlas v2 alignment)

- `real-estate-workflow`, `healthcare-workflow` — overlap with Atlas v2; revisit when a real-estate or healthcare Nexus tenant exists.

---

## What changes for users at each phase

| Phase | What a member experiences differently |
|---|---|
| **B-1** | "Draft an announcement" / "summarize this doc" produces consistent, well-structured output. Decisions made in chat get logged to memory automatically. |
| **B-2** | Chat threads can be turned into structured meeting notes with one ask. Friday digests appear automatically. New tasks all follow the same conventions. |
| **B-3** | New members get an instant templated welcome with workspace-specific info (free, no LLM). If they ask `@Brain walk me through`, the workspace pays for a tailored LLM walkthrough. Bugs reported in chat become triaged tasks. Memory stays organized over time without manual cleanup. |
| **B-4** | Multi-step requests produce real plans that Brain executes step-by-step, with verification before claiming "done". Trust in Brain's reports goes up sharply. |
| **B-5** | Brain answers in Spanish or English (auto-detected) with LATAM-appropriate register (`tú`/`vos`/`usted`, country-specific norms). |
| **C** | Brain proposes new skills based on patterns it's seen — the catalog grows organically per workspace. |

---

## Sources

- [Anthropic — Skills overview](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview)
- [anthropics/skills](https://github.com/anthropics/skills) — 17 published skills, 4 pre-built, 13 upload-yourself
- [obra/superpowers](https://github.com/obra/superpowers) — battle-tested workflow skills, source for Phase B-4 forks
- [VoltAgent/awesome-agent-skills](https://github.com/VoltAgent/awesome-agent-skills) — community discovery index
- [NousResearch hermes-agent](https://github.com/NousResearch/hermes-agent) — reference for procedural-memory + per-user-modeling patterns
- v3 architecture in this repo: `internal/brain3/`, system prompt at `internal/brain3/system_prompt.go`, memory layout at `internal/brain3/memory.go`
