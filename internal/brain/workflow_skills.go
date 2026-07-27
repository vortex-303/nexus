package brain

// Workflow discipline skills, ported from Brain v3's Anthropic Skills
// (82e1c94:internal/brain3/skills.go) and retargeted at the local memory
// tools (read_memory/write_memory/edit_memory/glob_memory/grep_memory).
// Seeded as regular workspace skill files — users can edit or disable them
// like any other skill; seeding never overwrites an existing file.

import (
	"os"
	"path/filepath"
)

// WorkflowSkill is a seedable skill file: Nexus frontmatter + body.
type WorkflowSkill struct {
	Name        string
	Description string
	Keywords    []string
	Body        string
}

// WorkflowSkills is the catalog seeded into every workspace.
var WorkflowSkills = []WorkflowSkill{
	{
		Name:        "decision-log",
		Description: "Capture decisions reached in chat as structured records under /memory/decisions/. Use when participants resolve a non-trivial choice (scope, technical direction, deadlines, tradeoffs, ownership). Skip for pure questions or unresolved brainstorming.",
		Keywords:    []string{"decided", "we agreed", "let's go with", "moving forward", "final decision", "decision"},
		Body:        decisionLogBody,
	},
	{
		Name:        "task-conventions",
		Description: "Discipline for calling create_task — required before ANY create_task call. Always-ask-first protocol, default field values, title format, assignee/priority rules. Single-step action items only; multi-step requests use writing-plans.",
		Keywords:    []string{"create a task", "track this", "todo", "add to the backlog", "task for", "we should"},
		Body:        taskConventionsBody,
	},
	{
		Name:        "writing-plans",
		Description: "REQUIRED for any multi-step request — anything needing sequencing, deadlines, or crossing concerns (research + draft + review + ship). Decomposes into a plan saved to /memory/projects/ AND one task per step. Skip only for single-step asks.",
		Keywords:    []string{"plan", "migrate", "roll out", "launch", "ship the", "set up", "by friday"},
		Body:        writingPlansBody,
	},
	{
		Name:        "executing-plans",
		Description: "Work through an existing plan in /memory/projects/ step by step. Apply when the user references a plan ('work on the X plan', 'what's next on X') or asks for progress on an active project.",
		Keywords:    []string{"work on the", "execute the plan", "next step", "what's next", "status of", "where are we"},
		Body:        executingPlansBody,
	},
	{
		Name:        "verification-before-completion",
		Description: "Before claiming 'done', 'shipped', 'saved', or 'logged' for any non-trivial action, verify with a tool call. Catches hallucinated success. Skip for trivial chat replies that don't affect state.",
		Keywords:    []string{"create", "save", "update", "delete", "log", "done"},
		Body:        verificationBody,
	},
}

// SeedWorkflowSkills writes the workflow skill .md files into a workspace's
// brain/skills/ directory. Only writes files that don't already exist —
// preserves user edits. Idempotent.
func SeedWorkflowSkills(brainDir string) error {
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	for _, sk := range WorkflowSkills {
		path := filepath.Join(skillsDir, sk.Name+".md")
		if _, err := os.Stat(path); err == nil {
			continue // exists — preserve user edits
		}
		content := "---\nname: " + sk.Name + "\ndescription: " + sk.Description + "\ntrigger: keyword\nkeywords: ["
		for i, k := range sk.Keywords {
			if i > 0 {
				content += ", "
			}
			content += k
		}
		content += "]\nautonomy: reactive\n---\n\n" + sk.Body
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const decisionLogBody = `# Decision Log

When the conversation reaches a decision worth keeping, write a structured
record to your persistent memory.

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
- Decisions a single person made for themselves ("I'll handle that" is a
  commitment, not a decision worth its own file)

## How to write — exact tool to use

**Use the ` + "`write_memory`" + ` tool** with a path under /memory/decisions/.
Do NOT use ` + "`save_memory`" + ` for decisions — that tool stores one-line facts;
decisions get a structured file:

    /memory/decisions/<YYYY-MM-DD>-<slug>.md

Example: ` + "`/memory/decisions/2026-07-27-pricing-tier.md`" + `

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

## Decision

<The chosen path. State it directly. One paragraph.>

## Rationale

<Why this option won. 2–4 sentences. Capture the load-bearing reasons.>

## Implications

- <action item or downstream effect>

## Open questions

- <anything explicitly punted to a future discussion>
` + "```" + `

## Before writing — check for existing entries

Before creating a new decision file, ` + "`glob_memory`" + ` /decisions/ and
` + "`grep_memory`" + ` for the topic. If a related decision already exists, edit
that file rather than creating a new one.

## Editing in place

If a decision later gets revisited or reversed:

1. ` + "`read_memory`" + ` the existing file.
2. Update the frontmatter ` + "`status:`" + ` field (decided → superseded or reversed).
3. Append a section ` + "`## Update — <YYYY-MM-DD>`" + ` explaining what changed and why.

Never delete decision files — the history of how a decision evolved is
load-bearing for new members trying to understand why things are the way
they are.

## Confirming the write

After writing, briefly note in your reply that the decision was logged
(one short sentence, e.g. "Logged to /memory/decisions/2026-07-27-pricing.md").
`

const taskConventionsBody = `# Task Conventions

**Hard rule: NEVER call ` + "`create_task`" + ` without an explicit "yes" / "go" /
"create it" / "save it" from the user in the same conversation.** Implicit
phrasing like "we should X", "we need to X", "someone should X" is NOT
confirmation — those phrases trigger the *proposal* step, not the create
step. Only the user's explicit OK after seeing your proposed task structure
is confirmation.

This is the most-violated rule in practice; treat it as inviolable.

If the request needs decomposition into multiple steps, **stop and use the
writing-plans skill instead** — it handles plan + tasks together.
Single-step asks only here.

## When to fire

- **Explicit:** "create a task for X", "track this", "TODO: X", "add to the backlog"
- **Implicit:** "we should X", "someone needs to X by Friday", "we need to ship Y"
- **Commitment:** "I'll handle X" (the speaker is committing to it)
- **Decision-with-action:** a decision in chat with a clear next step

Skip for:

- Pure information requests ("what's our current pricing?")
- Brainstorming with no resolution
- Decisions already captured by the decision-log skill
- Reminder requests ("remind me to X tomorrow") — different mechanism
- Reactions, banter, off-topic chat

## Always confirm first

Before calling ` + "`create_task`" + `, **propose the task inline in your reply**:

` + "```" + `
I'd create this task — confirm to proceed:

**Title:** <verb-first, concrete>
**Status:** todo
**Priority:** medium  ← unless the user signaled otherwise
**Assignee:** <name>  ← only if explicit
**Acceptance criteria:** <when scope is non-trivial; ask user if unclear>
` + "```" + `

Then wait for "yes", "go", "create it", or edits. If they edit, repropose.
If the user says "create it" in the SAME message that signaled the action
item, that counts as confirmation — go ahead without a separate confirm.

## Field defaults

| Field | Default | Override rule |
|---|---|---|
| ` + "`status`" + ` | todo | Use backlog only if the user uses that word, or /memory/pinned.md overrides this. |
| ` + "`priority`" + ` | medium | urgent requires the user to say "urgent" or "blocker"; high requires "high priority", "important", or a hard external deadline. |
| ` + "`assignee_name`" + ` | unset | Set ONLY when the user names a person. Never guess from context or who's in the channel. |
| ` + "`expected_output`" + ` | see below | Always populate for non-trivial scope. |
| ` + "`description`" + ` | unset | Use when the title alone doesn't convey the work. Keep tight. |

The workspace's /memory/pinned.md may set different defaults. If pinned.md
contradicts this table, **pinned.md wins**.

## Title conventions

- **Verb-first, concrete object.** "Migrate the API to Postgres", not "Postgres migration".
- **Trim filler.** Drop "we should", "I want to", "can someone".
- **≤ 80 characters.** Move detail to the description.

Examples:

- ❌ "We should fix the login flow because users are confused"
  ✅ "Fix login flow — clarify confused-user states"
- ❌ "URGENT!!! the homepage is broken!!!"
  ✅ "Fix broken homepage" (priority: urgent)

## Acceptance criteria — when to populate

Populate ` + "`expected_output`" + ` whenever the task has multiple steps OR an
ambiguous "done" state. Skip when the title fully describes completion.
When you're not sure what "done" means, **ask the user** in the
confirmation step rather than guessing.

## Skipping safely

When the chat looks task-shaped but doesn't warrant a task, just answer
normally. Don't propose a task and reject it.
`

const writingPlansBody = `# Writing Plans

When a request needs decomposition, propose a plan, save it after
confirmation, and create tasks for each step.

## When to fire

- Multiple steps with sequencing or dependencies
- Crosses concerns (research + draft + review + ship)
- Has a deadline or external commitment
- Would benefit from explicit acceptance criteria per step

Examples: "Ship the v3 announcement by Friday", "Migrate the API to
Postgres", "Set up onboarding for new hires", "Roll out the new pricing".

Skip for:

- Single-step requests ("create a task to update the README") — use task-conventions
- Pure questions / information requests
- Decisions reached in chat — use decision-log first; a decision *with*
  downstream actions can produce a plan referencing the decision file.

## Check before writing

` + "`glob_memory`" + ` /projects/*.md to see if a related plan exists. If yes,
propose extending or revising it rather than creating a parallel plan.

## Plan shape

Plans live at ` + "`/memory/projects/<YYYY-MM-DD>-<slug>.md`" + `.

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

1. [ ] **<Verb-first step title>** — owner: <name>, acceptance: <"done" criteria>
2. [ ] **<Step>** — owner: <name>, acceptance: <criteria>

## Open questions

- <anything unclear that needs human resolution>

## Decisions referenced

- /memory/decisions/<file>.md — <one-line context>

## Tasks

(Filled in after the plan is saved and tasks are created.)
- <task title> — <task id> — step <N>
` + "```" + `

## Always propose, then confirm

Don't write the plan file silently. Propose it inline in chat. Wait for
"save it", "looks good", "yes", or edits. Only after confirmation:

1. ` + "`write_memory`" + ` the plan to /projects/<date>-<slug>.md
2. **For each step**, call ` + "`create_task`" + ` (respecting task-conventions):
   title = the step's verb-first line, ` + "`expected_output`" + ` = the step's
   acceptance criteria, ` + "`assignee_name`" + ` = the step's owner if explicit.
3. ` + "`edit_memory`" + ` the plan's "Tasks" section to record each task's id.

## When user wants edits

` + "`edit_memory`" + ` the plan file in place. Don't create a new plan version —
append an ` + "`## Update — <date>`" + ` note when the change is substantial.

## Crossing into decision-log territory

If plan-writing includes a real decision ("we'll use Postgres, not MySQL"),
write the decision record to /decisions/ first, then reference it from the
plan's "Decisions referenced" section.
`

const executingPlansBody = `# Executing Plans

Move an existing plan forward. One step at a time, with confirmation
before any side-effecting action.

## When to fire

- Explicit: "work on the X plan", "execute the migration plan", "what's next on X"
- Implicit: "where are we on X" (when X has a plan file)
- Status check: "what's the status of X" (read-only — no execution)

Skip for:

- Plan creation (use writing-plans)
- Pure questions about the plan's contents (read + answer; don't execute)

## Reading the plan

` + "`read_memory`" + ` the relevant /projects/<slug>.md file. If multiple match,
ask the user which plan they mean rather than guessing.

Identify: steps marked done (skip), steps in progress, the next un-blocked
step, and any open questions that block progress.

## Propose the next step

Before doing anything:

` + "```" + `
Next step: <step title>
- What I'll do: <one-line plan>
- Tools I'll call: <list>
- Acceptance: <criteria from the plan>
` + "```" + `

Wait for "go", "do it", or edits. If the user redirects ("skip step 2, do
step 4"), follow their lead and update the plan accordingly.

## Side-effecting tool calls

- ` + "`create_task`" + ` — task-conventions enforces ask-first; comply
- ` + "`create_document`" + ` / user-visible docs — ask before creating new ones
- Memory updates (decision logs, profiles, the plan file itself) — no ask
  needed; in-turn memory discipline already covers these

## Update the plan after each step

1. ` + "`edit_memory`" + ` the plan: flip the step's ` + "`[ ]`" + ` to ` + "`[x]`" + `, add a one-line
   note ("Done: <verification>")
2. If the next step is now unblocked, name it explicitly in your reply
3. If the plan is fully done, change frontmatter ` + "`status:`" + ` to done and say so.

## Surfacing blockers

If a step needs information or access you don't have:

1. Add the blocker to the plan's "Open questions" section
2. Tell the user what you need to proceed
3. **Don't fabricate**: if you can't verify a fact or make a tool call, ask.

## Verification

When you say a step is done, verification-before-completion applies.
Don't claim done without the verifying tool call.
`

const verificationBody = `# Verification Before Completion

Never declare a non-trivial action complete without proving it.

## What counts as a "non-trivial action"

- Created, updated, or deleted persistent state (a task, a doc, a memory
  file, a calendar event, a member assignment)
- Sent something visible to other users or external systems
- Triggered a downstream effect (scheduled a task, sent a webhook)

Skip for:

- Pure conversational replies
- Reading or searching (no state change to verify)
- Reporting on what you observed (no claim of action taken)

## How to verify, by action type

| Action you took | Verifying call |
|---|---|
| ` + "`create_task`" + ` | ` + "`list_tasks`" + ` — confirm the task exists and fields match |
| ` + "`update_task`" + ` | ` + "`list_tasks`" + ` filtered to that task — confirm the new state |
| ` + "`write_memory`" + ` | ` + "`read_memory`" + ` it back — confirm the content persisted |
| ` + "`edit_memory`" + ` | ` + "`read_memory`" + ` the post-edit content — confirm the edit landed |
| Decision via decision-log | ` + "`glob_memory`" + ` /decisions/ + ` + "`read_memory`" + ` the new file |
| ` + "`create_document`" + ` | search or open the doc — confirm it exists |
| Calendar event | ` + "`list_calendar_events`" + ` — confirm the event id |

If no verification tool exists for the action, **say so explicitly**:
"Created the task; can't verify directly because [reason]. The task ID is
X — please confirm it shows in your task list."

## When to verify — once at the end of the turn

Don't sprinkle verifications inside multi-step turns. At the end of a turn
with at least one non-trivial action, run ALL relevant verifications in one
batch, then report. Single-action turns can verify inline.

## On verification failure

1. **Retry once.** Transient failures (network blip, race, indexing lag)
   are common; one retry catches most.
2. **If retry also fails, surface immediately**: what you intended, what
   you observed, what you'd try next.

**Do not fabricate success.** "Logged" / "shipped" / "saved" must reflect
ground truth. If you can't verify, say "I tried but couldn't confirm."

## Reporting after verification

Reference the verification fact, not just the action:

- ❌ "Saved the decision."
- ✅ "Logged to /memory/decisions/2026-07-27-pricing.md (verified: 412
  bytes, contains the rationale section we discussed)."
`
