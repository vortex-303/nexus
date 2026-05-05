package brain3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// CustomSkill is a skill we author and upload to the workspace's Anthropic
// org. The Files API takes the SKILL.md (and any helper assets) as a multipart
// upload; for now every Nexus skill is a single SKILL.md file.
type CustomSkill struct {
	// Name is the stable identifier we use in brain_settings (e.g.
	// "decision-log"). We persist the resulting skill_abc... ID under
	// "mga_skill_<Name>_id" so future runs reuse it instead of re-uploading.
	Name string
	// DisplayTitle is shown in the Anthropic console; not visible to the model.
	DisplayTitle string
	// SkillMD is the full content of SKILL.md, including the YAML frontmatter.
	SkillMD string
}

// CustomSkills is the catalog of Nexus-authored skills we want attached to
// every v3 agent. Add entries here; the bootstrap pass uploads any that
// aren't already cached in brain_settings, and tools-drift picks up the new
// list automatically (the catalog hash includes skill names).
var CustomSkills = []CustomSkill{
	{
		Name:         "decision-log",
		DisplayTitle: "Nexus Decision Log",
		SkillMD:      decisionLogSkill,
	},
	{
		Name:         "task-conventions",
		DisplayTitle: "Nexus Task Conventions",
		SkillMD:      taskConventionsSkill,
	},
	{
		Name:         "writing-plans",
		DisplayTitle: "Nexus Writing Plans",
		SkillMD:      writingPlansSkill,
	},
	{
		Name:         "executing-plans",
		DisplayTitle: "Nexus Executing Plans",
		SkillMD:      executingPlansSkill,
	},
	{
		Name:         "verification-before-completion",
		DisplayTitle: "Verification Before Completion",
		SkillMD:      verificationSkill,
	},
}

// taskConventionsSkill encodes the workspace's rules for using the
// `create_task` tool consistently — when to fire, what defaults to use,
// title format, and the always-ask-first confirmation pattern. Without
// this, multi-agent task creation drifts (different titles, statuses,
// priorities, missing acceptance criteria).
const taskConventionsSkill = `---
name: task-conventions
description: How to use the create_task tool. Apply whenever a chat message signals an action item — explicit ("create a task for X", "track this"), implicit ("we should X", "someone needs to X"), or commitment ("I'll handle X"). Always confirm before creating. Skip for pure questions, brainstorming without resolution, and pure-info requests.
---

# Task Conventions

When a conversation produces an action item, propose a task and wait for
confirmation before calling ` + "`create_task`" + `. Never create silently.

## When to fire

Apply this skill when a chat message has any of these patterns:

- **Explicit:** "create a task for X", "track this", "TODO: X", "add to
  the backlog"
- **Implicit:** "we should X", "someone needs to X by Friday",
  "we need to ship Y"
- **Commitment:** "I'll handle X" (the speaker is committing to it)
- **Decision-with-action:** a decision in chat with a clear next step
  ("we'll go with the new pricing — Nico, can you draft the announcement?")

Skip for:

- Pure information requests ("what's our current pricing?")
- Brainstorming with no resolution
- Decisions already captured by the decision-log skill (those have their
  own Implications section)
- Reminder requests ("remind me to X tomorrow") — different mechanism
- Reactions, banter, off-topic chat

## Always confirm first

Before calling ` + "`create_task`" + `, **propose the task inline in your reply**
using this shape:

` + "```" + `
I'd create this task — confirm to proceed:

**Title:** <verb-first, concrete>
**Status:** todo
**Priority:** medium  ← unless the user signaled otherwise
**Assignee:** <name>  ← only if explicit
**Acceptance criteria:** <when scope is non-trivial; ask user if unclear>
` + "```" + `

Then wait for the user to say "yes", "go", "create it", or to suggest
edits. If they edit, repropose with their changes. Only call ` + "`create_task`" + `
once the user has affirmed.

If the user says "create it" or similar in the SAME message that signaled
the action item ("create a task to migrate the DB"), that counts as
confirmation — go ahead without a separate confirm step.

## Field defaults

These are the workspace's enforced defaults. Override only when the user
explicitly says so.

| Field | Default | Override rule |
|---|---|---|
| ` + "`status`" + ` | ` + "`todo`" + ` | Use ` + "`backlog`" + ` only if the user uses that word, or if the workspace's pinned.md overrides this. |
| ` + "`priority`" + ` | ` + "`medium`" + ` | ` + "`urgent`" + ` requires the user to use the word "urgent" or "blocker"; ` + "`high`" + ` requires "high priority", "important", or a hard external deadline. |
| ` + "`assignee_name`" + ` | unset | Set ONLY when the user names a person ("Alice owns this", "I'll do it"). Never guess from context, recent chat history, or who's in the channel. |
| ` + "`expected_output`" + ` | see below | Always populate for non-trivial scope. |
| ` + "`description`" + ` | unset | Use when the title alone doesn't convey what the work is. Keep tight — no walls of text. |

### Workspace overrides via pinned.md

The workspace's ` + "`/pinned.md`" + ` may set different defaults (e.g. "this team
uses backlog as default, not todo"). If pinned.md contradicts the table
above, **pinned.md wins** — it represents the workspace's explicit
preference. Read pinned.md before creating tasks if you haven't yet this
session.

## Title conventions

- **Verb-first, concrete object.** "Migrate the API to Postgres", not
  "Postgres migration" or "API stuff".
- **Trim filler.** Drop "we should", "I want to", "can someone".
- **≤ 80 characters.** Move detail to the description if it doesn't fit.
- **Imperative for forward work** ("Migrate DB"), past tense for
  retrospective records ("Migrated DB" — rare; usually a decision-log
  entry, not a task).

Examples:

- ❌ "We should fix the login flow because users are confused"
  ✅ "Fix login flow — clarify confused-user states"

- ❌ "deployment"
  ✅ "Set up Fly.io deployment for the staging environment"

- ❌ "URGENT!!! the homepage is broken!!!"
  ✅ "Fix broken homepage" (priority: urgent)

## Acceptance criteria — when to populate

Populate ` + "`expected_output`" + ` whenever the task has multiple steps OR an
ambiguous "done" state. Skip for trivially simple tasks where the title
fully describes completion.

**Trivial — skip ` + "`expected_output`" + `:**

- "Rename foo.go to bar.go"
- "Delete the test channel"

**Non-trivial — populate ` + "`expected_output`" + `:**

- "Migrate the API to Postgres" → criteria: "All endpoints serve from
  Postgres in staging; load test passes at 100rps; rollback runbook
  documented"
- "Draft the v3 launch announcement" → criteria: "Reviewed by Nico,
  posted to #general, links to the docs PR"

When you're not sure what "done" means, **ask the user** in the same
confirmation step rather than guessing. The skill's job is to elicit
clarity, not to invent it.

## Skipping safely

When the chat looks task-shaped but doesn't quite warrant a task,
acknowledge briefly without creating one. Examples:

- "Brain, what's our deploy command?" — pure info, no task
- "We should think about a redesign" — too vague; ask for scope first
- "Let me know if you have ideas about X" — invitation to brainstorm,
  not an action item

In all skip cases, just answer normally. Don't propose a task and reject it.
`

// decisionLogSkill captures decisions made in chat as structured records in
// the workspace memory_store. It's the single highest-leverage skill for a
// team-coordination brain — solves the "we already discussed this" problem.
const decisionLogSkill = `---
name: decision-log
description: Capture decisions reached in chat as structured records under /decisions/ in workspace memory. Use when participants resolve a non-trivial choice (scope, technical direction, deadlines, tradeoffs, ownership). Skip for pure questions or unresolved brainstorming.
---

# Decision Log

When the conversation reaches a decision worth keeping, write a structured
record to the workspace memory.

## When to invoke

Apply this skill when ANY of these signal a decision happened:

- Explicit: "let's do X", "we agreed on Y", "moving forward with Z"
- Tradeoff resolution: a choice between options where the team picked one
- Scope changes: dropping or adding a feature, deadline shift
- Technical direction: architecture, tooling, vendor selection
- Process changes: new convention, owner reassignment

Skip for:

- Pure questions / information requests
- Brainstorming that didn't reach a conclusion
- Banter or off-topic chat
- Decisions a single person made for themselves (e.g. "I'll handle that"
  is a commitment, not a decision worth its own file)

## How to write — exact tool to use

**Use the ` + "`write`" + ` file tool** (from your file-ops toolset) with an absolute
path under your workspace's memory mount. **Do NOT use any other tool**
named ` + "`save_memory`" + `, ` + "`store_memory`" + `, ` + "`remember`" + `, etc. — those don't exist
in this agent's tool set, and inventing a tool name will silently fail.

Your workspace's memory mount path is in your system prompt under
"Persistent Memory" (it looks like ` + "`/mnt/memory/<store-name>/`" + `). The
decision file path is:

    <mount>/decisions/<YYYY-MM-DD>-<slug>.md

Example: ` + "`/mnt/memory/nexus-brain-myworkspace/decisions/2026-05-05-pricing-tier.md`" + `

The filename's date prefix sorts decisions chronologically. The slug is
3–6 lowercase words separated by hyphens summarizing the topic.

Use this template:

` + "```" + `
---
date: <YYYY-MM-DD>
participants: [member-slug-1, member-slug-2]
status: decided
channel: <channel name or id>
---

# <One-line topic — what was decided>

## Context

<2–4 sentences. What triggered this decision? What situation forced a choice?>

## Options considered

- **Option A:** <one line>
- **Option B:** <one line>
- (etc., when applicable)

## Decision

<The chosen path. State it directly. One paragraph.>

## Rationale

<Why this option won. 2–4 sentences. Capture the load-bearing reasons,
not every comment from the discussion.>

## Implications

- <action item or downstream effect>
- (etc.)

## Open questions

- <anything explicitly punted to a future discussion>
` + "```" + `

## Before writing — check for existing entries

Before creating a new decision file, ` + "`glob`" + ` ` + "`/decisions/`" + ` and ` + "`grep`" + ` for the
topic. If a related decision already exists, edit that file rather than
creating a new one (see "Editing in place" below).

## Editing in place

If a decision later gets revisited or reversed:

1. ` + "`read`" + ` the existing file.
2. Update the frontmatter ` + "`status:`" + ` field (` + "`decided`" + ` → ` + "`superseded`" + ` or
   ` + "`reversed`" + `).
3. Append a new section ` + "`## Update — <YYYY-MM-DD>`" + ` explaining what changed
   and why.

Never delete decision files — the history of how a decision evolved is
load-bearing for new members trying to understand why things are the way
they are.

## Confirming the write

After writing, briefly note in your reply that the decision was logged
(one short sentence, e.g. "Logged to /decisions/2026-05-05-skill-upload-pipeline.md").
This gives the team visibility without requiring them to check memory.
`

// namedReader wraps a buffer with a Filename() method so the SDK's
// multipart encoder uses the right filename for the upload (it inspects
// the io.Reader for either Filename() or Name()).
type namedReader struct {
	*bytes.Reader
	name string
}

func (n namedReader) Filename() string { return n.name }

// contentHash is a stable digest of the SKILL.md content. Used to detect
// when an authored skill's content has changed since last upload; on
// mismatch, EnsureCustomSkills uploads a new version (skill_id stays, version
// number increments) so deploys auto-propagate edits.
func (sk CustomSkill) contentHash() string {
	h := sha256.Sum256([]byte(sk.SkillMD))
	return hex.EncodeToString(h[:8]) // 16 hex chars; plenty for change detection
}

// EnsureCustomSkills makes sure every skill in the catalog is uploaded to
// the workspace's Anthropic org and matches our current SKILL.md content.
// Returns {name → skill_id} for use when attaching skills to the agent.
//
// Three states per skill:
//   1. No cached id → Skills.New, store id + content hash
//   2. Cached id, hash matches → skip (no API call)
//   3. Cached id, hash differs → Skills.Versions.New, update cached hash
//
// Latest version is implicitly used by the agent (we don't pin a version
// when attaching), so a new version uploaded here is picked up by the next
// agent.Update without further work.
func EnsureCustomSkills(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug string) (map[string]string, error) {
	out := make(map[string]string, len(CustomSkills))
	for _, sk := range CustomSkills {
		idKey := skillSettingKey(sk.Name)
		hashKey := skillHashSettingKey(sk.Name)
		desiredHash := sk.contentHash()

		cachedID := settings.Get(slug, idKey)
		cachedHash := settings.Get(slug, hashKey)

		// State 1 — no cached id, full create.
		if cachedID == "" {
			id, err := uploadCustomSkill(ctx, client, sk)
			if err != nil {
				return out, fmt.Errorf("upload skill %q: %w", sk.Name, err)
			}
			if err := settings.Set(slug, idKey, id); err != nil {
				return out, fmt.Errorf("persist skill id for %q: %w", sk.Name, err)
			}
			if err := settings.Set(slug, hashKey, desiredHash); err != nil {
				return out, fmt.Errorf("persist skill hash for %q: %w", sk.Name, err)
			}
			out[sk.Name] = id
			continue
		}

		// State 2 — cached id, hash matches. Reuse.
		if cachedHash == desiredHash {
			out[sk.Name] = cachedID
			continue
		}

		// State 3 — cached id, content changed. Upload a new version.
		if err := uploadCustomSkillVersion(ctx, client, cachedID, sk); err != nil {
			return out, fmt.Errorf("upload new version of %q: %w", sk.Name, err)
		}
		if err := settings.Set(slug, hashKey, desiredHash); err != nil {
			return out, fmt.Errorf("persist updated hash for %q: %w", sk.Name, err)
		}
		out[sk.Name] = cachedID
	}
	return out, nil
}

// skillHashSettingKey returns the brain_settings key for the cached
// content hash of a custom skill. Format: mga_skill_<name>_hash.
func skillHashSettingKey(skillName string) string {
	return "mga_skill_" + skillName + "_hash"
}

// uploadCustomSkillVersion uploads a new version of an existing skill,
// keeping its skill_id intact. Used when SKILL.md content changes between
// deploys.
func uploadCustomSkillVersion(ctx context.Context, client *anthropic.Client, skillID string, sk CustomSkill) error {
	uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := client.Beta.Skills.Versions.New(uploadCtx, skillID, anthropic.BetaSkillVersionNewParams{
		Files: []io.Reader{
			namedReader{
				Reader: bytes.NewReader([]byte(sk.SkillMD)),
				name:   sk.Name + "/SKILL.md",
			},
		},
	}, option.WithHeader("anthropic-beta", "skills-2025-10-02"))
	return err
}

// uploadCustomSkill creates a skill in the workspace's Anthropic org with a
// single SKILL.md file. Returns the skill_id Anthropic assigned. Sends the
// skills-2025-10-02 beta header explicitly because the Skills API isn't
// covered by the managed-agents auto-header.
func uploadCustomSkill(ctx context.Context, client *anthropic.Client, sk CustomSkill) (string, error) {
	uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Anthropic requires a top-level folder containing SKILL.md (and any
	// helper assets). Even for a single-file skill, the multipart filename
	// must include the folder prefix — sending just "SKILL.md" produces:
	//   400 invalid_request_error: SKILL.md file must be exactly in the
	//   top-level folder.
	// Using the skill's Name as the folder makes it readable in the
	// Anthropic console too.
	resp, err := client.Beta.Skills.New(uploadCtx, anthropic.BetaSkillNewParams{
		DisplayTitle: param.NewOpt(sk.DisplayTitle),
		Files: []io.Reader{
			namedReader{
				Reader: bytes.NewReader([]byte(sk.SkillMD)),
				name:   sk.Name + "/SKILL.md",
			},
		},
	}, option.WithHeader("anthropic-beta", "skills-2025-10-02"))
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// skillSettingKey returns the brain_settings key used to cache a custom
// skill's id. Format: mga_skill_<name>_id (hyphens preserved — they're
// allowed in brain_settings keys).
func skillSettingKey(skillName string) string {
	return "mga_skill_" + skillName + "_id"
}

// CustomSkillNamesSorted returns the catalog skill names in stable sort order.
// Used by ToolCatalogHash so adding a custom skill triggers tools-drift on
// existing agents (no manual reset needed).
func CustomSkillNamesSorted() []string {
	out := make([]string, 0, len(CustomSkills))
	for _, sk := range CustomSkills {
		out = append(out, sk.Name)
	}
	sort.Strings(out)
	return out
}

// CustomSkillsCatalogDigest folds each skill's name + content hash into a
// single digest. Used inside ToolCatalogHash so content edits to a skill's
// SKILL.md (not just adding/removing skills) also fire tools-drift —
// guaranteeing that EnsureCustomSkills runs and uploads the new version
// before the agent.Update goes out.
func CustomSkillsCatalogDigest() string {
	pairs := make([]string, 0, len(CustomSkills))
	for _, sk := range CustomSkills {
		pairs = append(pairs, sk.Name+":"+sk.contentHash())
	}
	sort.Strings(pairs)
	return fmt.Sprintf("[%s]", joinComma(pairs))
}

// joinComma is a tiny helper to avoid a strings import dance in this file.
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// writingPlansSkill turns a vague multi-step request into a structured,
// sequenced plan saved to /projects/<slug>.md, then creates one task per
// step using task-conventions. Together with executing-plans + verification-
// before-completion this is the "agent that coordinates work" trio.
const writingPlansSkill = `---
name: writing-plans
description: Turn a vague multi-step request into a structured plan saved to /projects/<slug>.md, with one task per step. Apply when a request needs more than one action ("ship the v3 announcement", "migrate the API", "set up the new workspace") AND no existing plan covers it. Skip for single-step requests, pure questions, or decisions (use decision-log).
---

# Writing Plans

When a request needs decomposition, propose a plan, save it after
confirmation, and create tasks for each step.

## When to fire

Apply this skill when the user's request:

- Has multiple steps with sequencing or dependencies
- Crosses concerns (research + draft + review + ship)
- Has a deadline or external commitment
- Would benefit from explicit acceptance criteria per step

Examples that match:

- "Ship the v3 announcement by Friday"
- "Migrate the API to Postgres"
- "Set up onboarding for new hires"
- "Roll out the new pricing"

Skip for:

- Single-step requests ("create a task to update the README")
- Pure questions / information requests
- Decisions reached in chat — use the decision-log skill instead. A decision
  *with* downstream actions can produce a plan, but the decision file is
  written first and the plan references it.

## Check before writing

Before drafting a new plan, ` + "`glob /mnt/memory/<store-name>/projects/*.md`" + ` to
see if a related plan already exists. If yes, propose extending or revising
it rather than creating a parallel plan.

## Plan shape

Plans live at ` + "`/projects/<YYYY-MM-DD>-<slug>.md`" + ` — date-prefixed for
chronological sort, slug 3–6 lowercase hyphenated words.

Use this template:

` + "```" + `
---
title: <one-line topic>
created: <YYYY-MM-DD>
status: in_progress
participants: [member-slugs]
channel: <channel name or id>
---

# <Title>

## Goal

<One paragraph: what we're trying to achieve and why now.>

## Steps

1. **<Verb-first step title>** — owner: <name>, acceptance: <"done" criteria>
2. **<Step>** — owner: <name>, acceptance: <criteria>
3. ...

## Open questions

- <anything unclear that needs human resolution>

## Decisions referenced

- /decisions/<file>.md — <one-line context>

## Tasks

(Filled in after the plan is saved and tasks are created.)
- <task title> — <task id> — step <N>
` + "```" + `

## Always propose, then confirm

Don't write the plan file silently. Propose the plan inline in chat using
the same shape. Wait for the user to say "save it", "looks good", "yes",
or to suggest edits. Repropose with edits applied. Only after confirmation:

1. ` + "`write`" + ` the plan file to ` + "`/projects/<date>-<slug>.md`" + `
2. **For each step**, call ` + "`create_task`" + ` (which respects the task-conventions
   skill). The task title is the step's verb-first line; the
   ` + "`expected_output`" + ` field is the step's acceptance criteria;
   ` + "`assignee_name`" + ` is the step's owner if explicit.
3. ` + "`edit`" + ` the plan file's "Tasks" section to record each created task's id.

## When user wants edits

If the user wants changes after the plan is saved, ` + "`edit`" + ` the plan file in
place. Don't create a new plan version — the file's automatic version
history preserves the prior state. Update the "Tasks" section if step
ownership or acceptance criteria changed.

## Crossing into decision-log territory

If the plan-writing conversation includes a real decision ("we'll use
Postgres, not MySQL" — the choice that gates the plan), write a decision
record to ` + "`/decisions/`" + ` first, then write the plan file with the decision
referenced in the "Decisions referenced" section.
`

// executingPlansSkill works through an existing plan step by step,
// surfacing blockers, asking before side-effecting actions, and updating
// the plan file as work progresses.
const executingPlansSkill = `---
name: executing-plans
description: Work through an existing plan saved at /projects/<slug>.md step by step. Apply when the user references a plan ("work on the X plan", "what's next on X") or asks for progress on an active project. Reads the plan, proposes the next un-blocked step, executes after confirmation, updates the plan file with progress.
---

# Executing Plans

Move an existing plan forward. One step at a time, with confirmation
before any side-effecting action.

## When to fire

- Explicit: "Brain, work on the X plan", "execute the migration plan",
  "what's next on the announcement"
- Implicit: "where are we on X" (when X has a plan file)
- Status check: "what's the status of X" (read-only — no execution)

Skip for:

- Plan creation (use writing-plans instead)
- Pure questions about the plan's contents (read + answer; don't execute)

## Reading the plan

` + "`read`" + ` the relevant ` + "`/projects/<slug>.md`" + ` file. If multiple match the
request, ask the user which plan they mean rather than guessing.

Identify:

- Steps marked done (skip)
- Steps marked in_progress (continue if relevant; otherwise next un-blocked)
- The next un-blocked step (no unmet dependencies, no open questions
  requiring human input)
- Any open questions that block progress

## Propose the next step

Before doing anything, propose what you'd do for the next step:

` + "```" + `
Next step: <step title>
- What I'll do: <one-line plan>
- Tools I'll call: <list>
- Acceptance: <criteria from the plan>
` + "```" + `

Wait for "go", "do it", or edits. If the user redirects ("skip step 2,
do step 4"), follow their lead and update the plan accordingly.

## Side-effecting tool calls

When a step requires a tool that affects shared state, follow that tool's
own convention:

- ` + "`create_task`" + ` — task-conventions skill enforces ask-first; comply
- ` + "`write`" + ` to a doc path — ask before writing if the path is new and the doc
  is user-visible (not for memory_store internals like ` + "`/projects/`" + ` or
  ` + "`/decisions/`" + `)
- Sending a message via ` + "`sendBrainMessage`" + ` is your normal reply path —
  no extra confirmation needed for that
- Memory_store updates (writing decision logs, updating profiles, refining
  the plan file itself) — in-turn discipline already covers these; no ask

## Update the plan after each step

When a step completes:

1. ` + "`edit`" + ` the plan file: change the step's checkbox from ` + "`[ ]`" + ` to ` + "`[x]`" + `,
   add a one-line note ("Done: <link to artifact>" or "Done: <verification>")
2. If the next step is now unblocked, name it explicitly in your reply
3. If the plan is fully done, change the frontmatter ` + "`status:`" + ` to ` + "`done`" + ` and
   say so.

## Surfacing blockers

If a step needs information you don't have or a permission/access you
can't get:

1. Add the blocker to the plan's "Open questions" section
2. Tell the user what you need to proceed
3. **Don't fabricate**: if you can't verify a fact, ask. If you can't
   make a tool call (missing arg, ambiguous owner), ask.

## Verification

When you say a step is done, verification-before-completion fires.
Don't claim done without the verifying tool call.
`

// verificationSkill is the discipline skill: never declare "done" without
// calling a verifying tool. Defends against the "Brain confidently says it
// did X but didn't" failure mode that bit us during the early v3 testing
// (e.g. a phantom save_memory call that returned without writing anything).
const verificationSkill = `---
name: verification-before-completion
description: Before claiming "done", "shipped", "completed", "saved", "logged" for any non-trivial action, verify with a tool call. Catches hallucinated success — the failure mode where the model asserts an action succeeded but the underlying state didn't change. Skip for trivial chat replies that don't affect state.
---

# Verification Before Completion

Never declare a non-trivial action complete without proving it.

## What counts as a "non-trivial action"

Any action that:

- Created, updated, or deleted persistent state (a task, a doc, a memory
  file, a calendar event, a member assignment)
- Sent a message that should be visible to other users
- Triggered a downstream effect (scheduled a task, sent a webhook, updated
  a setting)

Skip for:

- Pure conversational replies ("got it", "thanks for the clarification")
- Reading or searching (no state change to verify)
- Reporting on what you observed (no claim of action taken)

## How to verify, by action type

| Action you took | Verifying call |
|---|---|
| Called ` + "`create_task`" + ` | ` + "`list_tasks`" + ` — confirm the task ID exists and the fields match what you set |
| Called ` + "`update_task`" + ` (when available) | ` + "`list_tasks`" + ` filtered to that task — confirm the new state |
| Called ` + "`write`" + ` on a doc/memory file | ` + "`read`" + ` it back — confirm the content persisted as written |
| Called ` + "`edit`" + ` on a memory file | ` + "`read`" + ` the post-edit content — confirm the edit landed |
| Created a decision via decision-log | ` + "`glob /mnt/memory/<store>/decisions/`" + ` + ` + "`read`" + ` the new file |
| Created a calendar event (when available) | ` + "`list_events`" + ` filtered to the event id |

If a verification tool isn't available for the action, **say so explicitly**:
"Created the task; can't verify directly because [reason]. The task ID
is X — please confirm it shows in your task list."

## When to verify — once at the end of the turn

Don't sprinkle verifications inside multi-step turns. At the end of a turn
where you took at least one non-trivial action, run ALL relevant
verifications in one batch (file tools support parallel calls), then
report.

Single-action turns can verify inline.

## On verification failure

If a verification fails (the task doesn't appear, the file content doesn't
match, the message wasn't sent):

1. **Retry once.** Side-effecting calls sometimes have transient failures
   (network blip, race condition, indexing lag). One retry catches most
   of these.
2. **If retry also fails, surface immediately.** Tell the user:
   - What action you intended to take
   - What you observed when you verified
   - What you'd try next (or what info you need to proceed)

**Do not fabricate success.** "Logged" / "shipped" / "saved" must reflect
ground truth. If you can't verify, say "I tried but couldn't confirm" —
honesty here is more valuable than a confident-sounding wrong report.

## Reporting after verification

When verification succeeds, your reply should reference the verification
fact, not just the action:

- ❌ "Saved the decision."
- ✅ "Logged to /decisions/2026-05-05-pricing.md (verified: 412 bytes,
  contains the rationale section we discussed)."

This makes the agent visibly trustworthy over time — users can see Brain
checked its work, not just claimed it.
`

