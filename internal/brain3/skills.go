package brain3

import (
	"bytes"
	"context"
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
}

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

## What to write

Create a file at ` + "`/decisions/<YYYY-MM-DD>-<slug>.md`" + ` inside the workspace
memory mount. The filename's date prefix sorts decisions chronologically.
The slug is 3–6 lowercase words separated by hyphens summarizing the topic.

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

// EnsureCustomSkills uploads any custom skills that aren't already recorded
// in brain_settings, and returns the full list of {name → skill_id} for the
// workspace. Idempotent: skills already uploaded (cached id present) are
// skipped without an API call.
func EnsureCustomSkills(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug string) (map[string]string, error) {
	out := make(map[string]string, len(CustomSkills))
	for _, sk := range CustomSkills {
		key := skillSettingKey(sk.Name)
		if id := settings.Get(slug, key); id != "" {
			out[sk.Name] = id
			continue
		}
		id, err := uploadCustomSkill(ctx, client, sk)
		if err != nil {
			return out, fmt.Errorf("upload skill %q: %w", sk.Name, err)
		}
		if err := settings.Set(slug, key, id); err != nil {
			return out, fmt.Errorf("persist skill id for %q: %w", sk.Name, err)
		}
		out[sk.Name] = id
	}
	return out, nil
}

// uploadCustomSkill creates a skill in the workspace's Anthropic org with a
// single SKILL.md file. Returns the skill_id Anthropic assigned. Sends the
// skills-2025-10-02 beta header explicitly because the Skills API isn't
// covered by the managed-agents auto-header.
func uploadCustomSkill(ctx context.Context, client *anthropic.Client, sk CustomSkill) (string, error) {
	uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.Beta.Skills.New(uploadCtx, anthropic.BetaSkillNewParams{
		DisplayTitle: param.NewOpt(sk.DisplayTitle),
		Files: []io.Reader{
			namedReader{
				Reader: bytes.NewReader([]byte(sk.SkillMD)),
				name:   "SKILL.md",
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
