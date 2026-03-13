# Living Briefs + Self-Reflection — Comprehensive Plan

## Vision

Brain becomes a **self-aware, self-improving agent** that continuously reflects on workspace activity, updates its own understanding, and produces **Living Briefs** — synthetic, always-current documents that make the workspace's intelligence visible internally and externally.

Two interconnected systems:
1. **Self-Reflection Engine** — Brain periodically analyzes its own performance, workspace patterns, and North Star alignment, writing findings back into its own core files
2. **Living Briefs** — Auto-generated, refreshable documents on custom topics, powered by reflection data + workspace context + behavioral analytics

---

## Part 1: Self-Reflection Engine

### What It Is

Brain runs a periodic reflection loop (separate from heartbeat) that:
1. **Observes** — Gathers behavioral data, activity patterns, conversation quality
2. **Reflects** — Analyzes what's working, what's not, how aligned work is with North Star
3. **Writes** — Updates its own MD files with learnings (new file: `REFLECTIONS.md`)
4. **Adapts** — Adjusts its behavior based on accumulated reflections

### New Brain Definition File: `REFLECTIONS.md`

Structured sections that Brain reads on every prompt and updates periodically:

```markdown
## Workspace Pulse
- Team velocity: 12 tasks completed this week (up from 8 last week)
- Most active: @nick (47 messages), @sarah (23 messages)
- Quiet: @mike (2 messages, last seen 3 days ago)
- Hot channels: #product (89 messages), #engineering (34 messages)
- North Star alignment: 7/10 — most tasks align, but 3 tasks seem off-mission

## What's Working
- Daily standups in #general getting good engagement
- Task completion rate improving week-over-week
- Knowledge base growing — 12 new docs this week

## Concerns
- @mike hasn't been active — may need check-in
- No progress on Q2 OKR "Launch API docs"
- 5 tasks overdue with no updates

## My Performance
- Response quality: Good on task queries, weaker on strategic questions
- Tool usage: Web search underutilized, could help with market research
- Memory: 142 active memories, last consolidation 2 days ago
- Missed opportunities: @sarah asked about competitor pricing — I had no data

## Learnings
- Team prefers bullet-point responses over long paragraphs
- @nick usually asks strategic questions on Mondays
- Technical questions get better responses when I cite specific docs
```

### Reflection Schedule

| Frequency | What | Output |
|-----------|------|--------|
| **Hourly** | Quick pulse — message counts, active members, new tasks | Internal metrics only (stored in DB) |
| **Daily** (configurable time) | Behavioral analysis, North Star alignment check, performance self-assessment | Updates `REFLECTIONS.md` |
| **Weekly** (configurable day) | Deep reflection — trends, patterns, team dynamics, strategic assessment | Updates `REFLECTIONS.md` + optional Living Brief |

### Behavioral Data to Track (New)

Some of this already exists in `activity_stream`. New tracking needed:

| Data Point | Source | New? |
|------------|--------|------|
| Messages per member per day | `activity_stream` pulse `message.sent` | Aggregate query — **existing** |
| Tasks created/completed per member | `activity_stream` pulse `task.*` | Aggregate query — **existing** |
| Response time (Brain mention → response) | `brain_action_log` | **New field**: `response_time_ms` |
| Member last seen / online status | Need new tracking | **New**: `member_last_active` table or column |
| Channel activity heatmap | `activity_stream` grouping | Aggregate query — **existing** |
| North Star alignment score | LLM-assessed per task/decision | **New**: computed during reflection |
| Brain tool usage frequency | `brain_action_log` `tools_used` | Aggregate query — **existing** |
| Conversation sentiment | LLM-assessed during reflection | **New**: computed during reflection |
| Memory utilization rate | `brain_memories` count + hit rate | **New**: track memory retrieval hits |
| Document staleness | `documents` `updated_at` age | Query — **existing** |
| Task velocity (created vs completed) | `tasks` timestamps | Query — **existing** |
| File upload patterns | `activity_stream` `file.uploaded` | Aggregate — **existing** |

### Implementation: Reflection Loop

```
┌─────────────────────────────────────────────────┐
│                REFLECTION LOOP                   │
│                                                  │
│  1. GATHER (no LLM needed)                      │
│     ├─ Query activity_stream (last N hours)      │
│     ├─ Query task stats (velocity, overdue)      │
│     ├─ Query member activity (messages, tasks)   │
│     ├─ Query brain_action_log (my performance)   │
│     ├─ Read current REFLECTIONS.md               │
│     └─ Read North Star settings                  │
│                                                  │
│  2. REFLECT (LLM call)                           │
│     ├─ "Given this data + North Star + previous  │
│     │   reflections, what's your assessment?"     │
│     ├─ Structured output: pulse, concerns,       │
│     │   learnings, self-assessment, alignment     │
│     └─ Keep it concise — this is for Brain,      │
│        not for humans                            │
│                                                  │
│  3. WRITE                                        │
│     ├─ Update REFLECTIONS.md with new findings   │
│     ├─ Merge with existing (don't lose history)  │
│     └─ Optionally update MEMORY.md with          │
│        high-confidence insights                  │
│                                                  │
│  4. ASSESS NORTH STAR                            │
│     ├─ Score current alignment (0-10)            │
│     ├─ List aligned vs misaligned activities     │
│     └─ Suggest course corrections                │
│                                                  │
└─────────────────────────────────────────────────┘
```

### Backend: `internal/server/brain_reflection.go` (new file)

```go
// Core functions:
func (s *Server) gatherReflectionData(slug string) ReflectionData
func (s *Server) runReflection(slug string, depth string) // "quick", "daily", "weekly"
func (s *Server) scheduleReflections() // called from server startup
func (s *Server) writeReflectionFile(slug, content string) error
```

**ReflectionData struct:**
```go
type ReflectionData struct {
    Period           string // "hourly", "daily", "weekly"
    MessagesByMember map[string]int
    TasksCreated     int
    TasksCompleted   int
    TasksOverdue     int
    ActiveMembers    []MemberActivity
    InactiveMembers  []MemberActivity
    ChannelActivity  map[string]int
    BrainActions     int
    ToolsUsed        map[string]int
    MemoryCount      int
    NorthStar        string
    PreviousReflection string
}
```

---

## Part 2: Living Briefs

### What It Is

Auto-generated, refreshable documents on any topic. Each brief is a **template** that Brain fills using reflection data + workspace context + North Star. Briefs can be:
- Viewed in-app (new page type)
- Shared as a public link (future: viral mechanic)
- Read aloud via Grok audio (future)
- Used as conversation starters with Brain

### Built-In Brief Templates

#### 1. "Workspace Pulse" (default)
*How is Brain doing? Who's working? What's happening?*

Sections:
- **Team Activity** — Who's active, who's quiet, engagement patterns
- **Work Velocity** — Tasks created vs completed, trend arrows
- **Hot Topics** — Most discussed channels/topics
- **Brain Status** — How many questions answered, tool usage, memory health
- **Upcoming** — Calendar events, approaching deadlines
- **Mood** — Overall workspace energy (derived from activity volume + sentiment)

#### 2. "North Star Status"
*How aligned are we with our mission?*

Sections:
- **Goal Recap** — The North Star in one line
- **Alignment Score** — X/10 with reasoning
- **Aligned Work** — Tasks/decisions that advance the mission
- **Drift Alerts** — Activities that seem off-mission
- **Strategic Themes Check** — Each theme rated: progressing / stalled / needs attention
- **Recommended Actions** — What to focus on next

#### 3. "Team Health"
*Behavioral analysis of the team*

Sections:
- **Member Spotlight** — Each active member: what they're working on, activity level, strengths observed
- **Collaboration Patterns** — Who talks to whom, cross-channel activity
- **Engagement Risks** — Members going quiet, declining activity
- **Workload Distribution** — Who has the most tasks, who might be overloaded

#### 4. "Project Brief" (custom topic)
*Status update on a specific project/goal*

Sections:
- **Objective** — What this project is about (user-defined or North Star derived)
- **Progress** — Tasks completed, milestones hit
- **Blockers** — Overdue tasks, unresolved questions
- **Key Decisions** — From memory: what was decided and why
- **Next Steps** — What Brain recommends

### Data Model

```sql
-- New table: living_briefs
CREATE TABLE living_briefs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    template TEXT NOT NULL,           -- 'workspace_pulse', 'north_star', 'team_health', 'custom'
    topic TEXT DEFAULT '',            -- custom topic description
    content TEXT DEFAULT '',          -- last generated markdown content
    generated_at DATETIME,
    schedule TEXT DEFAULT 'manual',   -- 'manual', 'daily', 'weekly'
    schedule_time TEXT DEFAULT '',    -- 'HH:MM' or 'monday HH:MM'
    share_token TEXT DEFAULT '',     -- for public sharing (future)
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Brief Generation Flow

```
┌──────────────────────────────────────────────────────┐
│              BRIEF GENERATION                         │
│                                                       │
│  1. GATHER CONTEXT (structured, no LLM)              │
│     ├─ ReflectionData (from reflection engine)        │
│     ├─ REFLECTIONS.md (Brain's latest self-assessment)│
│     ├─ North Star settings                            │
│     ├─ Template-specific queries:                     │
│     │   ├─ Team Health → member stats, activity       │
│     │   ├─ North Star → task alignment, themes        │
│     │   └─ Custom → relevant memories, channel data   │
│     └─ Previous brief content (for continuity)        │
│                                                       │
│  2. GENERATE (LLM call)                              │
│     ├─ System: "You are generating a Living Brief"    │
│     ├─ Template-specific prompt with all context      │
│     ├─ Style: engaging, synthetic, friendly           │
│     ├─ Tone: like a smart teammate giving an update   │
│     └─ Format: markdown with headers, bullets, emojis │
│                                                       │
│  3. STORE & DELIVER                                   │
│     ├─ Save to living_briefs.content                  │
│     ├─ Broadcast via WebSocket (live update)          │
│     └─ Optional: post summary to a channel            │
│                                                       │
└──────────────────────────────────────────────────────┘
```

### Frontend: Briefs Page

New route: `/w/{slug}/briefs`

- List of configured briefs (cards with title, last generated, template type)
- Click → full brief view (rendered markdown)
- "Refresh" button → regenerate now
- "New Brief" → pick template or custom topic
- Each brief shows "Last updated: 5 minutes ago" with auto-refresh indicator
- Future: "Share" button → generates public link
- Future: "Discuss" button → opens chat with Brain pre-loaded with brief context

### Sidebar Entry

Add "Briefs" to Pages nav section (between Activity and Social Pulse).

---

## Part 3: Connecting Everything

### The Flow

```
North Star (mission) ──────────────────────────────────────┐
                                                            │
Workspace Activity ──→ Activity Stream ──→ Reflection ──→ REFLECTIONS.md
  (messages, tasks,     (existing)         Engine          (new brain file)
   files, events)                          (new)               │
                                                               │
                                                               ▼
                                                         Living Briefs
                                                          (generated)
                                                               │
                                                               ▼
                                                     ┌─────────────────┐
                                                     │  Internal View  │
                                                     │  (Briefs page)  │
                                                     ├─────────────────┤
                                                     │  Public Share   │
                                                     │  (future)       │
                                                     ├─────────────────┤
                                                     │  Audio Brief    │
                                                     │  (future: Grok) │
                                                     ├─────────────────┤
                                                     │  Chat w/ Brain  │
                                                     │  (future)       │
                                                     └─────────────────┘
```

### How Self-Reflection Improves Brain

1. **REFLECTIONS.md is loaded into every system prompt** (like SOUL.md, INSTRUCTIONS.md)
   - Brain always knows the current team pulse
   - Brain can reference "I noticed @mike has been quiet" without being asked
   - Brain can say "this doesn't align with our North Star" proactively

2. **Reflection updates MEMORY.md** with high-confidence insights
   - "Team prefers bullet-point responses" → Brain adapts its style
   - "Strategic questions peak on Mondays" → Brain can anticipate

3. **Reflection catches drift**
   - North Star says "focus on API docs" but no one is working on it → flagged
   - A member's workload is growing unsustainably → flagged

---

## Implementation Phases

### Phase 1: Self-Reflection Foundation
**Files:** `internal/server/brain_reflection.go`, `internal/brain/brain.go`

1. Add `REFLECTIONS.md` to brain definition files (EnsureDefaults, BuildSystemPrompt)
2. Build `gatherReflectionData()` — aggregate queries on existing tables
3. Build `runReflection()` — LLM call that produces REFLECTIONS.md content
4. Schedule daily reflection (configurable, default 3:00 AM UTC)
5. Add `reflection_enabled` to brain_settings + UI toggle
6. Add `member_last_active` tracking (update on message send)
7. Add `response_time_ms` to brain_action_log

### Phase 2: Living Briefs Backend
**Files:** `internal/server/briefs.go`, `internal/db/migrations/`

1. Add `living_briefs` table (migration)
2. CRUD endpoints: create, read, update, delete, generate
3. Built-in templates with structured prompts
4. Brief generation using reflection data + workspace context
5. Schedule-based auto-generation (daily/weekly)
6. WebSocket broadcast on generation

### Phase 3: Living Briefs Frontend
**Files:** `web/src/routes/(app)/w/[slug]/briefs/+page.svelte`

1. Briefs list page with cards
2. Brief detail view (rendered markdown)
3. New brief wizard (template picker + custom topic)
4. Refresh / regenerate button
5. Sidebar entry in Pages nav
6. Brief settings (schedule, template config)

### Phase 4: North Star Integration
1. "North Star Status" brief template
2. Alignment scoring in reflection loop
3. Drift alerts surfaced in workspace pulse brief
4. North Star referenced in all brief generation prompts

### Phase 5: Sharing & Engagement (Future)
1. Public share links with token auth
2. Embeddable brief widgets
3. Audio briefs via text-to-speech
4. "Discuss this brief" → opens Brain chat with brief context
5. Brief history / diff view (how things changed over time)

---

## Key Design Decisions

1. **REFLECTIONS.md is a brain file, not a brief** — It's Brain's internal journal. Briefs are the polished output for humans.

2. **Reflection is cheap** — The data gathering is pure SQL queries. Only the synthesis step needs an LLM call. Use the cheapest capable model (memory_model setting).

3. **Briefs are regenerated, not accumulated** — Each generation replaces the previous content. History is optional (Phase 5).

4. **Self-reflection reads North Star** — Every reflection cycle checks alignment. This makes North Star truly active, not just a passive prompt injection.

5. **Tone matters** — Briefs should feel like a smart colleague giving you a friendly update, not a corporate dashboard. Engaging, concise, with personality.

6. **Progressive complexity** — Start with daily reflection + workspace pulse brief. Add templates and customization later.
