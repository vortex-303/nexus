# Reactive Brain — Making Nexus Feel Alive

## The Pitch

Right now, Brain only speaks when spoken to. You @mention it, it responds. But a real team brain doesn't wait to be asked — it notices things, connects dots, and intervenes when it matters.

**Reactive Brain** makes Nexus the first team platform where the AI is always aware of what's happening and acts when it should — without being annoying.

---

## Core Concept: Workspace Pulse

Every meaningful action in the workspace (message sent, task completed, file uploaded, deadline missed) flows through a lightweight **pulse** — a single function call in existing handlers that asks: "Should Brain or an agent care about this?"

No event bus. No pub/sub infrastructure. No YAML configs. Just smart, contextual reactions wired directly into the code that already runs.

```go
// One function. Called from existing handlers. That's the entire "event system."
func (s *Server) onPulse(slug string, pulse Pulse) {
    go s.evaluatePulse(slug, pulse)
}
```

---

## Phase 1: Pulse Infrastructure (Lightweight)

### 1a: Pulse Type

```go
// internal/server/pulse.go
type Pulse struct {
    Type      string         // "task.overdue", "message.created", etc.
    ChannelID string         // where it happened
    ActorID   string         // who did it
    ActorName string         // display name
    Source    string         // "user", "brain", "agent", "system"
    Data      map[string]any // event-specific payload
}
```

### 1b: Emission Points

Add `s.onPulse()` calls to existing handlers — no new files, no new packages:

| Handler | Pulse Type | When |
|---|---|---|
| `ws.go` handleWSSendMessage | `message.created` | User sends a message |
| `ws.go` handleWSEditMessage | `message.edited` | User edits a message |
| `tasks.go` handleCreateTask | `task.created` | Task created |
| `tasks.go` handleUpdateTask | `task.updated` | Task status/assignee changed |
| `tasks.go` handleUpdateTask | `task.completed` | Status changed to "done" |
| `channels.go` handleCreateChannel | `channel.created` | New channel |
| `channels.go` handleKickChannelMember | `member.removed` | Member kicked |
| `documents.go` handleUpdateDocument | `document.updated` | Doc content changed |
| `calendar.go` handleCreateCalendarEvent | `event.created` | Meeting scheduled |
| `calendar.go` reminder ticker | `event.reminder` | Reminder fires |
| `files.go` handleFileUpload | `file.uploaded` | File uploaded |
| `ingest.go` ingestExternalMessage | `integration.received` | Webhook/email/telegram arrives |
| `members.go` on join | `member.joined` | New member joins workspace |

### 1c: Pulse Router

```go
func (s *Server) evaluatePulse(slug string, pulse Pulse) {
    // Never react to Brain's own actions (prevent loops)
    if pulse.Source == "brain" || pulse.Source == "agent" {
        return
    }

    // 1. Check workspace reactions (admin-configured)
    s.checkReactions(slug, pulse)

    // 2. Check agent watchers
    s.checkAgentWatchers(slug, pulse)

    // 3. Feed the Activity Stream
    s.recordActivity(slug, pulse)

    // 4. Check overdue/deadline triggers (system)
    s.checkSystemTriggers(slug, pulse)
}
```

---

## Phase 2: Reactions (Admin-Configured Triggers)

Simple "when X happens, do Y" rules. No YAML, no code — configured in the Brain UI.

### What Users See

A new **Reactions** tab in Brain Configuration:

```
┌─────────────────────────────────────────────────────┐
│  Reactions                                     [+]  │
├─────────────────────────────────────────────────────┤
│                                                     │
│  ⚡ Task goes overdue                               │
│     → Brain posts reminder in task's channel        │
│     → Assigns to: channel where task was created    │
│     Active ✓                                        │
│                                                     │
│  ⚡ Meeting scheduled                               │
│     → Brain drafts agenda doc and posts link        │
│     → Assigns to: #general                          │
│     Active ✓                                        │
│                                                     │
│  ⚡ New member joins                                │
│     → Brain sends welcome DM with workspace guide   │
│     Active ✓                                        │
│                                                     │
│  ⚡ File uploaded to #design                        │
│     → @Creative Director reviews and gives feedback │
│     Active ✓                                        │
│                                                     │
│  ⚡ Task completed                                  │
│     → Brain posts celebration + updates summary     │
│     Active ✗                                        │
│                                                     │
└─────────────────────────────────────────────────────┘
```

### Data Model

```sql
CREATE TABLE brain_reactions (
    id TEXT PRIMARY KEY,
    trigger_type TEXT NOT NULL,        -- "task.overdue", "member.joined", etc.
    trigger_filter TEXT DEFAULT '',     -- JSON: {"channel_id": "...", "priority": "urgent"}
    action_type TEXT NOT NULL,         -- "brain_respond", "agent_mention", "create_task", "post_message"
    action_config TEXT DEFAULT '{}',   -- JSON: {"channel_id": "...", "agent_id": "...", "template": "..."}
    is_active BOOLEAN DEFAULT TRUE,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL
);
```

### Built-in Reaction Templates

Ship these as defaults that admins can toggle on/off:

| Trigger | Action | Why It's Killer |
|---|---|---|
| `task.overdue` | Brain posts reminder in channel, tags assignee | Teams never miss deadlines silently |
| `event.created` | Brain creates agenda doc, links in channel | Every meeting starts prepared |
| `member.joined` | Brain sends personalized welcome DM | Onboarding feels human from day one |
| `task.completed` | Brain posts to channel + updates project summary | Visibility into progress without standups |
| `integration.received` (webhook) | Brain triages and routes to right channel/person | Incoming alerts get smart routing |
| `document.updated` | Brain generates changelog summary | "What changed?" answered instantly |
| `event.reminder` (15 min before) | Brain posts attendee list + agenda + open tasks | Meetings start informed |

### Reaction Execution

```go
func (s *Server) checkReactions(slug string, pulse Pulse) {
    wdb, _ := s.ws.Open(slug)
    rows, _ := wdb.DB.Query(
        "SELECT id, trigger_filter, action_type, action_config FROM brain_reactions WHERE trigger_type = ? AND is_active = TRUE",
        pulse.Type,
    )
    // For each matching reaction, check filter, execute action
}
```

Actions go through existing Brain infrastructure — tool calls, agent mentions, message posting. No new execution engine needed.

---

## Phase 3: Agent Watchers

Agents can subscribe to pulse types. When a matching event fires, the agent receives context and responds.

### Agent Config Extension

Add to agent configuration:

```json
{
  "watch_events": ["task.created", "file.uploaded"],
  "watch_channels": ["design", "marketing"],
  "watch_filter": { "priority": "urgent" }
}
```

### What This Enables

**QA Agent** watches `task.completed` → automatically reviews and creates test checklist

**Design Review Agent** watches `file.uploaded` in #design → provides feedback on uploaded assets

**Security Agent** watches `integration.received` → scans webhook payloads for anomalies

**Onboarding Agent** watches `member.joined` → runs personalized onboarding sequence over days

**Deadline Agent** watches `task.created` with due dates → estimates feasibility, flags conflicts with calendar

### Implementation

```go
func (s *Server) checkAgentWatchers(slug string, pulse Pulse) {
    agents := s.loadAgentsWithWatchers(slug, pulse.Type)
    for _, agent := range agents {
        if !matchesFilter(agent.WatchFilter, pulse) {
            continue
        }
        // Build context message from pulse data
        contextMsg := formatPulseForAgent(pulse)
        // Trigger agent in the relevant channel
        go s.handleAgentMention(slug, pulse.ChannelID, "", contextMsg, "", agent, false)
    }
}
```

### DB Change

```sql
ALTER TABLE agents ADD COLUMN watch_events TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agents ADD COLUMN watch_channels TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agents ADD COLUMN watch_filter TEXT NOT NULL DEFAULT '{}';
```

---

## Phase 4: Activity Stream

A real-time feed of everything happening in the workspace. Think GitHub activity feed but for your entire team.

### What Users See

New **Activity** page in the sidebar (or a panel):

```
┌─────────────────────────────────────────────────┐
│  Activity                          Filter ▾     │
├─────────────────────────────────────────────────┤
│                                                 │
│  2 min ago                                      │
│  ✅ Sarah completed "Design landing page"       │
│     in #marketing                               │
│                                                 │
│  5 min ago                                      │
│  📝 Mike updated "Q1 Strategy" doc              │
│     Changed: added budget section               │
│                                                 │
│  12 min ago                                     │
│  📧 Email received from client@acme.com         │
│     Subject: "Contract feedback"                │
│     → Brain routed to #acme-project             │
│                                                 │
│  18 min ago                                     │
│  🤖 Brain created 3 tasks from standup          │
│     in #engineering                             │
│                                                 │
│  25 min ago                                     │
│  📅 Team sync scheduled for tomorrow 2pm        │
│     by Sarah · 4 attendees                      │
│                                                 │
│  31 min ago                                     │
│  🎨 Creative Director generated ad concept      │
│     "Matrix: Code Breaker" in DM                │
│                                                 │
│  1h ago                                         │
│  👋 Alex joined the workspace                   │
│     Brain sent welcome guide                    │
│                                                 │
└─────────────────────────────────────────────────┘
```

### Data Model

```sql
CREATE TABLE activity_stream (
    id TEXT PRIMARY KEY,
    pulse_type TEXT NOT NULL,
    channel_id TEXT,
    actor_id TEXT NOT NULL,
    actor_name TEXT NOT NULL,
    summary TEXT NOT NULL,           -- Human-readable one-liner
    detail TEXT DEFAULT '',          -- Extra context (JSON)
    source TEXT NOT NULL,            -- user, brain, agent, system
    created_at TEXT NOT NULL
);
CREATE INDEX idx_activity_created ON activity_stream(created_at DESC);
```

### API

```
GET /api/workspaces/{slug}/activity?limit=50&before=...&type=task.*
```

### WebSocket Event

```json
{ "type": "activity.new", "payload": { "id": "...", "pulse_type": "task.completed", "summary": "Sarah completed \"Design landing page\"", "created_at": "..." } }
```

### Why This Is Killer

- **Async teams get instant visibility** — no need to scroll through every channel
- **Managers see the whole picture** — who's doing what, where things are happening
- **Brain actions are visible** — transparency into what the AI is doing and why
- **Replaces daily standups** — the activity stream IS the standup

---

## Phase 5: Daily Digest

Brain generates a daily summary of workspace activity — posted to a channel or sent via email/Telegram.

### What Users See

Every morning at 9am (configurable), Brain posts to #general:

```
📊 Daily Digest — March 8, 2026

Yesterday your team:
• Completed 7 tasks (3 by Sarah, 2 by Mike, 2 by Alex)
• Created 4 new tasks (2 urgent)
• Updated 3 documents
• Received 5 webhook events from GitHub
• Had 2 meetings (Product Sync, Design Review)

⚠️ Attention needed:
• 2 tasks are overdue: "API docs" (3 days), "Fix login bug" (1 day)
• Meeting "Client Demo" is tomorrow at 3pm — no agenda doc yet
• 12 unread emails in #client-inbox

🔥 Highlights:
• "Landing page redesign" shipped — Sarah closed 5 related tasks
• New member Alex joined and completed onboarding
• Creative Director generated 8 ad concepts for Matrix campaign

📋 Today's calendar:
• 10:00 — Team standup (5 attendees)
• 14:00 — Sprint planning (3 attendees)
• 16:00 — Client call (2 attendees, agenda ready ✓)
```

### Implementation

Uses the `activity_stream` table + existing Brain tools (list_tasks, list_calendar_events). Triggered by the existing heartbeat scheduler — just add a new HEARTBEAT.md entry that references the activity data.

### Delivery Channels

- Post to configured channel (default: #general)
- Send via email (if outbound SMTP configured)
- Send via Telegram (if bot configured)
- All three simultaneously if configured

---

## Phase 6: Smart Notifications

Instead of getting pinged for every message, Brain curates what matters to YOU.

### What Users See

A notification bell in the sidebar that shows Brain-curated alerts:

```
┌───────────────────────────────────────┐
│  🔔 For You                     3 new │
├───────────────────────────────────────┤
│                                       │
│  🔴 Your task "API docs" is overdue   │
│     Due 2 days ago · #engineering     │
│                                       │
│  🟡 Sarah mentioned a decision about  │
│     the auth system that affects you   │
│     "We're switching to JWT..." · #eng│
│                                       │
│  🟢 Meeting "Sprint Planning" in 15m  │
│     Agenda: 4 items · 3 attendees     │
│                                       │
└───────────────────────────────────────┘
```

### How It Works

Brain evaluates each pulse against each member's context:
- Their assigned tasks
- Their channels
- Their role / reports_to
- Their recent activity

This runs on a lightweight heuristic — NOT an LLM call per event. Only call the LLM for ambiguous "should I notify?" decisions.

### Notification Rules (Zero-LLM)

| Condition | Notify | Priority |
|---|---|---|
| Your task is overdue | Always | High |
| Your task was completed by someone else | Always | Low |
| Meeting you're attending starts in 15min | Always | Medium |
| You were @mentioned | Always (already works) | High |
| A decision was made in a channel you follow | If keyword match | Medium |
| A task was assigned to you | Always | Medium |
| A file was uploaded to your channel | If you're the only member | Low |

### Data Model

```sql
CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    pulse_type TEXT NOT NULL,
    channel_id TEXT,
    title TEXT NOT NULL,
    body TEXT DEFAULT '',
    priority TEXT DEFAULT 'low',  -- low, medium, high
    read BOOLEAN DEFAULT FALSE,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_notifications_member ON notifications(member_id, read, created_at DESC);
```

### WebSocket Event

```json
{ "type": "notification.new", "payload": { "id": "...", "title": "Your task \"API docs\" is overdue", "priority": "high" } }
```

---

## Phase 7: Workspace Insights (Brain-Powered Analytics)

Brain analyzes activity patterns and surfaces insights weekly.

### What Users See

Weekly post from Brain (or on-demand via "how's the team doing?"):

```
📈 Weekly Insights — Week of March 3

Velocity:
• 23 tasks completed (up 15% from last week)
• Average task lifetime: 2.3 days (down from 3.1 — improving!)
• 4 tasks have been in "in_progress" for 5+ days

Collaboration:
• Most active channel: #engineering (342 messages)
• Quietest channel: #marketing (12 messages — down 80%)
• Brain handled 47 tool calls across 18 conversations

Bottlenecks:
• Sarah has 12 tasks assigned (highest on team)
• 3 tasks are blocked waiting on "API review" from Mike
• No one has updated the "Q1 Roadmap" doc in 2 weeks

Integrations:
• 23 GitHub webhooks processed
• 8 client emails received, 5 replied to by Brain
• 3 Telegram conversations active
```

### Implementation

Weekly cron job queries `activity_stream` + `tasks` + `messages` tables, generates stats, sends to Brain for natural language summary. Uses existing tools — no new infrastructure.

---

## Implementation Roadmap

| Phase | Effort | Impact | Visibility |
|---|---|---|---|
| **Phase 1: Pulse infrastructure** | 2-3 hours | Foundation | None (internal) |
| **Phase 2: Reactions** | 4-6 hours | High | High — "Nexus noticed my task was overdue" |
| **Phase 3: Agent Watchers** | 3-4 hours | High | High — agents that act without being asked |
| **Phase 4: Activity Stream** | 4-6 hours | Very High | Very High — everyone's favorite page |
| **Phase 5: Daily Digest** | 2-3 hours | High | High — replaces standups |
| **Phase 6: Smart Notifications** | 4-6 hours | Very High | Very High — personal relevance |
| **Phase 7: Workspace Insights** | 3-4 hours | Medium | High — managers love this |

**Recommended order:** Phase 1 → 4 → 2 → 5 → 3 → 6 → 7

Start with the Activity Stream (Phase 4) — it's the most visible, most useful, and easiest to demo. It makes the workspace feel alive before Brain even reacts to anything.

---

## Why This Beats an Event Bus

| Event Bus Approach | Reactive Brain Approach |
|---|---|
| New package, new types, new channels | One function, one struct, inline calls |
| Subscribers, fanout, buffered channels | Direct goroutine per pulse |
| YAML automation config | UI-configured reactions |
| Generic infrastructure | Purpose-built features |
| "We built a pub/sub system" | "Brain noticed your task was overdue and reminded you" |
| Engineers appreciate it | Users feel it |

The event bus is an implementation detail. The features above are what users pay for.

---

## Files to Create/Modify

### New Files
- `internal/server/pulse.go` — Pulse type + evaluatePulse + checkReactions + checkAgentWatchers
- `internal/server/activity.go` — Activity stream handlers (list, record, digest)
- `internal/server/notifications.go` — Smart notification handlers

### Modified Files
- `internal/server/ws.go` — Add `s.onPulse()` after message send/edit/delete
- `internal/server/tasks.go` — Add `s.onPulse()` after task CRUD
- `internal/server/channels.go` — Add `s.onPulse()` after channel create/kick
- `internal/server/documents.go` — Add `s.onPulse()` after doc update
- `internal/server/calendar.go` — Add `s.onPulse()` after event CRUD + reminder
- `internal/server/files.go` — Add `s.onPulse()` after file upload
- `internal/server/ingest.go` — Add `s.onPulse()` after external message
- `internal/server/agents.go` — Add watch_events, watch_channels, watch_filter fields
- `internal/server/server.go` — Register activity + notification routes
- `internal/db/migrations/migrations.go` — New tables (brain_reactions, activity_stream, notifications)
- `web/src/routes/(app)/w/[slug]/+page.svelte` — Activity panel, notification bell, reactions UI
- `web/src/lib/api.ts` — New API functions
- `web/src/lib/stores/workspace.ts` — Notification store

### Zero New Packages
Everything lives in `internal/server/` — no `internal/events/`, no `internal/automation/`. The existing architecture handles it.
