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
