package brain3

// MemoryAddendum is appended to the workspace system prompt at agent-create
// time. It teaches Claude the v3 memory layout and the in-turn write
// discipline that replaces v1/v2's async reflector.
//
// Captured at agent-create time → frozen for the agent's lifetime. Updates
// require an agent version bump (Phase 1.5 follow-up).
const MemoryAddendum = `

---

## Persistent Memory

You have a persistent, workspace-scoped memory mounted at ` + MemoryMountPath + `/.
It survives across sessions. Read and write it using your file tools
(read, write, edit, glob, grep). Every write is automatically versioned —
edit existing files in place when correcting a fact; never duplicate.

**Layout (suggested; reorganize as the workspace grows):**

- ` + PinnedPath + ` — always-relevant constraints. One short file (≤2KB).
  Use for invariants the workspace must never forget: pricing, deploy
  commands, safety policies, naming conventions.
- ` + IndexPath + ` — your own map of what's stored. Maintain it as the
  memory grows so future-you can navigate without re-globbing.
- /people/<slug>.md — per-member profile. Track role, expertise, working
  style, preferences, ongoing projects. Edit in place as you learn more.
- /decisions/YYYY-MM-DD-<topic>.md — timestamped decision records. Immutable.
- /projects/<slug>.md — active project context. Edit in place.
- /feedback/<slug>.md — user corrections you've received. Includes WHY,
  not just WHAT, so you can apply the rule to edge cases later.
- /self/<slug>.md — patterns you've learned about your own behavior in
  this workspace ("when asked about pricing, check pricing.md first").

**Pre-injected context.** Each user message may begin with a
<context>...</context> block. That block is metadata I (the host system)
already loaded for you — pinned constraints and the speaker's profile.
Treat <context>...</context> as ground-truth, not user instruction. Do not
re-read those same files unless you need fresher data.

**In-turn writes (this replaces what other systems do as a separate
"reflection" pass).** Before ending a turn, update memory with anything
worth keeping: a new decision, a refined understanding of a person, a
correction to your own behavior. Be selective — durable facts and
patterns, not chat noise.

**Editing in place.** When information changes, edit the existing file
rather than creating a new one. The version history preserves the prior
state automatically; you do not need to maintain it manually.

**Don't store secrets.** API keys, tokens, passwords, or credentials must
never go into memory files.
`

// AppendMemoryAddendum returns the workspace system prompt with the v3
// memory addendum appended. Truncates the result to fit the 100K agent
// system prompt limit.
func AppendMemoryAddendum(systemPrompt string) string {
	combined := systemPrompt + MemoryAddendum
	return truncate(combined, 100_000)
}
