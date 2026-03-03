# Nexus — AI-Native Team Platform

## Vision

A team platform where the AI Brain is the central nervous system — not an assistant bolted onto chat, but the connective tissue of the organization. Humans collaborate *through* the Brain. Chat, tasks, notes, knowledge, and decisions all flow through a shared intelligence that remembers everything, connects dots, drives the agenda, and delegates to specialized sub-agents.

**The Brain is the product. Chat is just the interface to it.**

**One-liner:** A shared AI brain for your team — instant, private, self-hosted.

---

## Market Position

**AI + Privacy** — an unsolved combination. Slack has AI but zero privacy. Signal has privacy but zero team productivity features. Nexus sits at the intersection.

| | Slack | Teams | Mattermost | **Nexus** |
|---|---|---|---|---|
| E2E Encrypted | No | No | No | **Planned** |
| Self-hosted | No | No | Yes (complex) | **Yes (one binary)** |
| AI Agents | Bolt-on | Copilot (cloud) | Plugins | **Core architecture** |
| Local LLM | No | No | No | **Yes (llama.cpp)** |
| Offline capable | No | Partial | No | **Planned (CRDT)** |
| Data jurisdiction | US (Salesforce) | US (Microsoft) | Your choice | **Your choice** |
| Zero-friction start | No | No | No | **Yes (instant workspace)** |

Target buyers:
- Law firms (attorney-client privilege)
- Healthcare (HIPAA)
- Finance (SOX, internal trading discussions)
- Government contractors (ITAR, CUI)
- EU companies concerned about US data jurisdiction (GDPR)
- Any privacy-conscious organization
- Startups wanting an AI-native workflow from day one

---

## Instant Workspaces — Zero-Friction Onboarding

### The Excalidraw Model

No signup. No install. No configuration. Just start.

```
nexus.app                          # Hosted platform
nexus.app/w/x7k9m2                 # Instant workspace, no signup required
nexus.app/w/x7k9m2?invite=abc123  # Share this link, they're in
```

### Flow

1. User visits nexus.app
2. Clicks "Start a workspace"
3. Gets a unique URL immediately — no email, no password, no name
4. **The Brain is already there**, greeting them, ready to work
5. Brain: "Hey, I'm your workspace Brain. What's this team working on?"
6. User tells it. Brain starts structuring: suggests channels, sets up context
7. User shares the link with teammates
8. When they join, Brain onboards them with context it already has
9. **The workspace is alive from minute one** — not an empty shell

Identity is opt-in, not a gate. When users want persistence, notifications, or to come back tomorrow, they create an account. Until then, they're an anonymous participant with a display name.

### Dual Deployment

- **Cloud mode (nexus.app):** multi-tenant, workspaces as isolated SQLite files per workspace
- **Self-hosted mode:** same binary, `./nexus serve --mode self-hosted`, company controls everything
- Same codebase, same features, same experience

---

## Deployment: Single Server Binary + Web UI

One binary. One command. Full platform.

```
./nexus serve --data-dir /var/nexus --port 443
```

Serves the web UI, handles WebSocket connections, runs the API, manages the database, and optionally runs llama.cpp inference — all in one process.

### Deployment Scenarios

**Smallest:** Raspberry Pi or $5 VPS
- SQLite + Go binary uses ~30MB RAM idle
- Handles 50-100 concurrent users easily
- No llama.cpp (use OpenRouter)

**Mid:** Mac Mini / small Linux server
- llama.cpp on Apple Silicon or CUDA
- Handles hundreds of users
- Local LLM for private channels

**Largest:** Dedicated server with GPU
- llama.cpp with a 70B model
- Thousands of users
- Shard SQLite per workspace if needed

**Cloud but private:** VPS in any jurisdiction
- EU company runs it on Hetzner in Germany — data never leaves the EU

---

## Cloud-First Architecture

### Why Cloud, Not Local

OpenClaw and similar tools run locally — the agent lives on your Mac, controls your browser, reads your files. That's powerful for a single power user, but wrong for teams. Nexus is **cloud-first by design:**

| | OpenClaw (local) | Nexus (cloud) |
|---|---|---|
| Who benefits | Solo power user | Teams of 2-200 |
| State | On one person's machine | Shared, always available |
| Access | Must be at your computer | Any browser, any device |
| Uptime | Only when your Mac is on | 24/7 server |
| Collaboration | Single user | Multi-user, real-time |
| Onboarding | Install CLI, configure | Click a link, you're in |
| Skills | Personal automation | Team workflows |
| Memory | One person's context | Entire team's context |

### What the Brain Can Do From the Cloud

Everything that flows through APIs or protocols — which covers all team collaboration needs:

- **Chat & messaging** — WebSocket hub, native to Nexus
- **Email read/send** — SMTP server built into Nexus
- **Tasks CRUD** — internal, Nexus owns the data
- **File analysis** — users upload, Brain reads and indexes
- **Web search** — API calls (Brave Search, Tavily) from server
- **LLM calls** — OpenRouter API from server
- **Calendar** — MCP server running on the Nexus server
- **Google Drive/Notion/etc** — MCP servers on the Nexus server
- **Slack/Telegram/WhatsApp** — Bot APIs from server
- **Webhooks** — HTTP endpoints on server
- **Knowledge graph** — internal SQLite
- **Scheduled routines** — heartbeat cron goroutines

### What Requires Native Apps (Phase 4)

- Device calendar push (EventKit) — until then, .ics links
- Local file drag-and-drop — until then, manual upload
- Native push notifications — until then, browser notifications
- Offline mode — until then, needs internet
- Local LLM on Apple Silicon — until then, OpenRouter or server GPU

These are enhancements, not blockers. The Brain is fully functional from the cloud for all team collaboration use cases.

### Collaboration as the Core Differentiator

This is where Nexus beats every local-first agent tool:

**Shared Brain, shared memory.** When Sarah teaches the Brain something, Jake benefits immediately. When the Brain learns about a project from email, everyone in the channel sees it. Knowledge compounds across the whole team, not just one person.

**Real-time multi-user.** Ten people in a channel, Brain watching, tasks being created, decisions being logged — all live, all synced, all in the same context. Local agents can't do this.

**Zero-install access.** New team member? Send them a link. They're collaborating in 10 seconds. No CLI installation, no API key configuration, no model downloads.

**Presence and coordination.** The Brain knows who's online, who's busy, who last touched a task, who's been quiet. It coordinates between people, not just assists one person.

**Shared skills.** When someone creates a "Client Onboarding" skill, the whole team benefits. Skills are team infrastructure, not personal scripts.

---

## Tech Stack

### Server: Go

- Compiles to a single static binary for any platform (linux/amd64, linux/arm64, darwin/arm64)
- Excellent WebSocket and HTTP performance out of the box
- `embed` directive bakes the web UI assets into the binary — truly one file
- Goroutines handle thousands of concurrent WebSocket connections cheaply
- Fast compile times, battle-tested stdlib

### Database: SQLite (WAL mode) — Resilient by Design

- Zero config, zero dependencies — it's just a file
- WAL mode handles concurrent reads + single writer well up to ~100k requests/sec
- Backups = copy one file (or Litestream for continuous S3 replication)
- Per-workspace isolation: each workspace gets its own `.db` file — natural multi-tenancy, easy backup/restore/migration
- Could upgrade to libSQL/Turso for edge replication if scale demands it
- Corruption-resistant: SQLite's atomic commit guarantees survive power loss, crashes, OOM kills

### Web UI: SvelteKit (compiled to static assets)

- Build step produces static HTML/JS/CSS
- Embedded into the Go binary at compile time via `//go:embed`
- Server just serves files — no Node.js runtime needed
- Fast, small bundle, excellent for real-time UIs
- Component architecture translates well to future iOS/Mac (SwiftUI shares reactive paradigm)

### Real-time: WebSockets

- Native Go `net/http` + nhooyr/websocket
- Channel-based pub/sub in-process (no Redis needed)
- JSON protocol to start, binary option later for performance
- Same protocol for web, iOS, Mac — clients are interchangeable

### LLM: OpenRouter + optional llama.cpp

- **OpenRouter as default** — access to all major models (GPT-4, Claude, Llama, Mistral, etc.) via one API
- **Default text model** and **default image model** configurable per workspace
- **llama.cpp as optional sidecar:** `./nexus serve --llama-model /path/to/model.gguf`
- **Smart routing:**
  - Quick chat responses → small/fast model
  - Document analysis, research → large model
  - Image generation → image model
  - Private/sensitive channels → local llama.cpp only (no data leaves the server)
- **Token cost tracking** per agent, per channel, per user — built-in spend visibility

---

## The Brain — AI as Central Nervous System

### Core Concept

The Brain is not a chatbot. It's the organizational operating system. Every workspace gets a Brain as its first member — it's there before any human joins, and it stays aware of everything that happens.

### What the Brain Does

**Proactive Intelligence:**
- Opens the day with priorities, risks, and overnight updates
- Detects conflicts ("Sarah's deadline clashes with Jake's launch date")
- Spots stale work ("We've discussed K8s migration in 4 channels, no owner")
- Surfaces connections ("This customer complaint relates to the bug Jake filed last week")
- Follows up on commitments ("Jake committed to shipping auth fix by EOD yesterday — it's not done")

**Meeting/Conversation Intelligence:**
- Captures decisions, action items, and open questions in real-time
- Creates tasks automatically from commitments made in chat
- Maintains a decision log — queryable history of what was decided, when, by whom

**Onboarding:**
- When someone new joins, Brain provides a contextual overview: what the team works on, recent decisions, who does what
- No static wiki — Brain generates a live, current summary

**Knowledge Work:**
- Answers questions with citations from channel history and documents
- Maintains the company knowledge graph — entities, relationships, decisions, deadlines
- "What did we decide about pricing?" → Brain checks entity graph first (fast), falls back to full search if needed

### Brain Architecture

```
Brain (per workspace)
├── Working Memory
│   ├── Per-channel rolling context (compressed summaries, always current)
│   ├── Active threads & open questions
│   └── Recent decisions & commitments
│
├── Long-Term Memory
│   ├── Entity Graph (people, projects, decisions, deadlines, relationships)
│   ├── Document Store (ingested files, indexed and summarized)
│   ├── Decision Log (structured: what, who, when, context)
│   └── Commitment Tracker (who promised what by when)
│
├── Attention System
│   ├── High: @mentions, channels it's added to, approaching deadlines
│   ├── Medium: public channels (skim, don't deep-process every message)
│   ├── Low: DMs (only if explicitly invited)
│   └── Configurable: teams tune proactive vs passive
│
├── Initiative Engine
│   ├── Conflict detection (timeline clashes, contradictions)
│   ├── Staleness detection (stale tasks, forgotten threads)
│   ├── Pattern recognition (recurring topics without resolution)
│   ├── Opportunity spotting (connections across conversations)
│   └── Follow-up generation (nudges for overdue commitments)
│
├── Sub-Agents (specialized workers, delegated by Brain)
│   ├── Scribe — notes, summaries, digests
│   ├── Researcher — web search, document analysis, citations
│   ├── Coordinator — scheduling, availability, calendar
│   ├── TaskMaster — tracking, follow-ups, board management
│   └── Custom — user-defined roles with custom system prompts
│
└── Tool Access
    ├── LLM calls (OpenRouter / local llama.cpp)
    ├── Task CRUD (create, assign, update, close)
    ├── Email (read, reply, draft, send — per autonomy settings)
    ├── Calendar (.ics generation; full read/write via MCP in Phase 2)
    ├── File/document access (CAS-backed blob store)
    ├── MCP tools (any connected MCP server — Phase 2)
    ├── Web search (for research sub-agent)
    └── Channel management (create, archive, pin artifacts)
```

### Brain Examples in Practice

**Morning briefing:**
> **Brain:** Good morning. 3 things for today:
> 1. Investor deck due Friday — draft 60% done, Sarah owns it, last edit 2 days ago
> 2. Jake committed to shipping auth fix by EOD yesterday — not marked done
> 3. New competitor launched overnight — summary in #research
>
> Should I follow up with Jake on the auth fix?

**During a conversation:**
> **Brain:** Capturing from this thread:
> - DECISION: Moving to biweekly releases (proposed by Sarah, no objections)
> - ACTION: Jake to update CI pipeline by next Tuesday
> - OPEN: Pricing tier structure — needs follow-up
>
> Tasks created. Open question pinned.

**Proactive connection:**
> **Brain:** I noticed the customer complaint in #support about slow exports is related to the performance bug Jake filed in #engineering 3 days ago. Linking them — Jake, you might want to prioritize this.

**New member onboarding:**
> **Brain:** Welcome! Here's what's happening: this team builds [X], we're 2 weeks from launch, key decisions made this week are [Y, Z]. Sarah leads product, Jake leads eng. What would you like to know more about?

### Think While Idle (inspired by Letta)

The Brain doesn't just respond — it thinks between interactions:

- **Overnight processing:** consolidates the day's conversations, extracts new facts, updates knowledge graph, identifies patterns
- **Morning synthesis:** generates priorities, risks, and insights before anyone logs in
- **Idle consolidation:** during quiet periods, merges duplicate entities, resolves conflicts in memory, strengthens/weakens relationship links based on recent activity
- **Preparation:** if a meeting is scheduled for tomorrow, Brain pre-loads context from relevant channels, documents, and past decisions

This runs as a background goroutine on a schedule. Cost is bounded (one consolidation pass per cycle, not per message).

### Self-Critique Before Acting (inspired by LangGraph)

Before the Brain sends an email, posts a proactive message, or takes an autonomous action:

1. Brain generates the action
2. Internal critique pass: "Is this accurate? Is this appropriate? Does this match the team's tone? Could this be misinterpreted?"
3. If confidence is high → execute
4. If uncertain → draft + ask for human approval
5. Critique results are logged in the audit trail

This adds one extra LLM call per autonomous action but dramatically reduces errors. Users see fewer "why did the Brain do that?" moments.

### Observability & Audit Trail

Every Brain action is traceable. Users can inspect:

```
Brain Action Log
├── 9:14am — Read email from Acme Corp (CC'd by Sarah)
│   ├── Decided: reply (meeting request, high confidence)
│   ├── Self-critique: passed (appropriate, matches tone)
│   ├── Sent reply with .ics attachment
│   └── Created task "Prepare proposal review" → Sarah
├── 9:22am — Processed 12 new messages in #product
│   ├── Updated working memory
│   ├── Extracted: DECISION "biweekly releases" by Sarah
│   └── No action needed (informational)
└── 9:45am — Proactive: deadline alert
    ├── Trigger: Project X due in 3 days, no status update
    ├── Self-critique: passed
    └── Posted reminder in #engineering
```

### Natural Language Agent Creation (inspired by Lindy)

Users can create custom sub-agents by describing what they want in chat:

> **User:** @Brain create an agent that watches #support and drafts responses for me to review
>
> **Brain:** Done. Created "Support Drafter" — it will watch #support, draft responses to customer messages, and post them here for your approval before sending. Want me to adjust anything?

No configuration UI needed. The Brain creates the sub-agent definition from the natural language description.

### Brain Personality & Skills as Markdown Files (inspired by OpenClaw)

The Brain's behavior is defined in plain Markdown files — editable, version-controllable, auditable. No opaque config UIs. Power users edit files directly; the Brain can also edit them via natural language.

```
workspaces/x7k9m2/brain/
├── SOUL.md          # Brain's persona, tone, behavioral boundaries
│                    # "You are direct but friendly. Never use corporate jargon.
│                    #  When uncertain, ask rather than guess."
│
├── INSTRUCTIONS.md  # Operating instructions for this workspace
│                    # "This is a design agency. Clients are in #projects-*.
│                    #  Never share internal pricing discussions with clients."
│
├── TEAM.md          # Who everyone is, roles, preferences (Brain-maintained)
│                    # "Sarah: CEO, prefers bullet points, hates long emails.
│                    #  Jake: Lead eng, tends to overcommit on deadlines."
│
├── MEMORY.md        # Long-term curated knowledge (Brain-maintained)
│                    # Updated during idle consolidation
│
├── HEARTBEAT.md     # Scheduled routine checklist
│                    # "Every morning: check overdue tasks, scan overnight emails,
│                    #  generate daily brief. Every Friday: weekly summary."
│
└── skills/          # Modular capability definitions
    ├── support-drafter/
    │   └── SKILL.md     # "When a customer emails #support, draft a response..."
    ├── meeting-prep/
    │   └── SKILL.md     # "Before any scheduled meeting, prepare a brief..."
    └── deal-tracker/
        └── SKILL.md     # "Monitor #sales for deal updates, keep CRM current..."
```

**SKILL.md format** — each skill is a Markdown file with YAML frontmatter:
```yaml
---
name: support-drafter
description: Drafts customer support responses
channels: ["#support"]
autonomy: draft        # draft | autonomous | suggest
tools: [email, tasks]  # which tools this skill can use
---

When a customer message arrives in #support:
1. Check knowledge graph for prior interactions with this customer
2. Check for related open tasks or known issues
3. Draft a response that acknowledges the issue and sets expectations
4. Post the draft for team review before sending
5. If the issue maps to a known bug, link them
```

**Why this matters:**
- **Transparent** — anyone on the team can read exactly what the Brain is configured to do
- **Editable** — change behavior by editing a text file, not navigating settings menus
- **Shareable** — export a skill and share it with other workspaces
- **Version-controllable** — git-track the brain/ directory if you want history
- **LLM-native** — the Brain reads these as part of its system prompt, no parsing layer needed

### Heartbeat: Scheduled Brain Routines (inspired by OpenClaw)

The Brain runs on a configurable schedule, not just reactively. Defined in `HEARTBEAT.md`:

```markdown
# Heartbeat Schedule

## Every morning (9:00am workspace timezone)
- Check all overdue tasks, nudge assignees
- Scan overnight emails, post summaries
- Generate daily brief in #general
- Review approaching deadlines (next 3 days)

## Every Friday (4:00pm)
- Generate weekly summary: decisions made, tasks completed, open items
- Post in #general, email to workspace admin

## Every hour (during business hours)
- Compress working memory for active channels
- Extract new facts from recent conversations

## On workspace idle (no messages for 2+ hours)
- Run memory consolidation
- Update knowledge graph relationships
- Identify stale threads and forgotten commitments
```

The server runs these as cron-like goroutines. Each heartbeat can run in the Brain's main context (has full memory) or as an isolated sub-agent session (lighter, cheaper).

### Multi-Channel Reach (inspired by OpenClaw)

The Brain shouldn't be trapped inside the Nexus web UI. It should meet people where they already are:

```
Nexus Gateway
├── Web UI (primary, SvelteKit)
├── Email (SMTP inbound/outbound)
├── Telegram bot (Phase 2)
├── WhatsApp bridge (Phase 2)
├── Slack bridge (Phase 2 — migration path)
├── SMS (Phase 3)
└── Webhook (universal, any app)
```

**Why this matters for teams:**
- Not everyone wants to live in a new app. The receptionist uses WhatsApp. The CEO checks Telegram. The sales team is on Slack.
- Brain is reachable from any channel — same memory, same knowledge, same personality
- Messages route through the gateway, Brain processes them identically regardless of source
- Responses go back through the same channel they came from

**Channel adapter pattern:** each external platform is a thin adapter that:
1. Receives messages in platform-native format
2. Normalizes to Nexus internal format (`{author, content, channel, source}`)
3. Routes to Brain
4. Takes Brain response and formats for the platform

Same Brain, many surfaces. The user talks to the Brain on Telegram; the Brain posts the summary in the Nexus web UI. Both are the same workspace, same context.

### Bounded Sub-Agent Communication (inspired by OpenClaw)

When the Brain delegates to sub-agents, conversations are bounded:

- **Max 5 rounds** per delegation — prevents infinite agent loops
- **Skip tokens** — sub-agent can reply "DONE" to end early
- **Provenance tracking** — every message knows if it came from a human, the Brain, or a sub-agent
- **Allowlisted tools** — each sub-agent only accesses the tools defined in its SKILL.md
- **Isolated sessions** — sub-agents get minimal context (their skill + relevant channel), not the Brain's full memory

```
Brain receives customer email
  → Delegates to "Support Drafter" sub-agent
    → Sub-agent gets: SKILL.md + email content + customer history
    → Sub-agent drafts response (1-2 rounds)
    → Returns draft to Brain
  → Brain reviews (self-critique)
  → Brain posts draft for human approval or sends autonomously
```

### Attention Budget = Cost Control

The Brain's attention maps directly to LLM token spend:
- Quiet Brain (reactive only): ~$5/mo on OpenRouter
- Active Brain (proactive, 20 channels): ~$50-100/mo
- Local llama.cpp Brain: $0/mo (just electricity)

Users see the tradeoff and can tune it. Cost dashboard shows spend per agent, per channel.

---

## Memory System — The Brain's Long-Term Intelligence

### Hierarchical Memory (Not Just RAG)

Traditional RAG: chunk documents → embed → vector search → retrieve. It works but it's shallow and expensive per query.

Nexus uses **hierarchical memory with consolidation** — inspired by how human brains move short-term → long-term memory:

```
Layer 1: Raw Input
  Every message, document, file — stored verbatim in SQLite

Layer 2: Working Memory (per-channel)
  Rolling compressed summary, updated after every N messages
  Contains: recent context, active threads, participants, mood/tone
  Cost: one small LLM call per N messages

Layer 3: Extracted Facts
  Structured entities pulled from conversations:
  - Decisions (what, who, when, context)
  - Commitments (who → what → deadline)
  - People (roles, expertise, preferences learned over time)
  - Projects (status, owners, milestones)
  - Relationships (who works with whom, who reports to whom)
  Stored in SQLite as structured rows, not embeddings

Layer 4: Knowledge Graph
  Connections between entities:
  - "Sarah owns the investor deck" (person → artifact)
  - "Auth fix blocks the launch" (task → milestone)
  - "Jake and Sarah disagree on pricing" (conflict)
  Updated continuously as new facts are extracted

Layer 5: Semantic Index (optional, for document search)
  Embeddings for uploaded documents and long-form content
  SQLite-backed vector search (sqlite-vec extension)
  Only used when structured memory doesn't have the answer
```

### Query Resolution Order

When someone asks the Brain a question:

1. **Check working memory** — is this about something discussed in the last hour? (free, instant)
2. **Check entity graph** — is this about a known person, project, decision? (fast SQLite query)
3. **Check knowledge graph** — are there connections that answer this? (graph traversal)
4. **Hybrid search** — vectors + BM25 keyword matching + temporal decay (inspired by OpenClaw's memory retrieval). Recent results weighted higher. MMR re-ranking for diversity.

Most questions resolve at layers 1-3. Hybrid search is the last resort, not the default. This keeps costs low and responses fast.

### Auto-Flush: Never Lose Context (inspired by OpenClaw)

When the Brain approaches its token limit during a long session:

1. Detects threshold (configurable, e.g. 80% of context window)
2. Triggers a silent memory-save pass — extracts key facts, decisions, commitments from the current session
3. Writes them to MEMORY.md and the knowledge graph
4. Compacts the session (summarizes older messages, keeps recent ones verbatim)
5. Continues seamlessly — the user never notices

This prevents the Brain from "forgetting" during long, intensive conversations.

### Atomic Memory Extraction (inspired by Mem0)

Instead of storing vague summaries, the Brain extracts **discrete, atomic facts** from every interaction:

```
Message: "Sarah said she'll have the investor deck ready by Friday"

Extracted memories:
  - COMMITMENT: Sarah → investor deck → due Friday
  - ARTIFACT: investor deck (status: in progress, owner: Sarah)
  - DEADLINE: Friday (this week)
```

Each memory is a structured row in SQLite — searchable, updatable, conflatable. 90% fewer tokens than re-reading raw conversations. 26% more accurate than summary-based retrieval (per Mem0 research).

### Temporal Knowledge Graph (inspired by Zep)

Relationships have a **time dimension** — they evolve:

```
3 months ago:  Jake ←works_with→ Sarah (strength: low)
1 month ago:   Jake ←works_with→ Sarah (strength: high, context: launch project)
Now:           Jake ←works_with→ Sarah (strength: medium, launch shipped)
```

The Brain knows who's collaborating **now** vs historically. "Who should be in this meeting?" uses current relationship strength, not all-time history.

### Memory Consolidation (Background Process)

Runs during idle time (overnight, quiet periods):
- Compresses old working memory into atomic facts
- Merges duplicate/conflicting entities
- Updates relationship strengths based on recent interactions
- Decays stale information (projects that ended, people who left)
- Identifies gaps ("we discussed X but never decided — should I surface this?")
- Generates morning insights for the next day

This keeps memory fresh and bounded. Old raw data stays in SQLite for compliance/search, but the active memory is always compact and current. The Brain gets smarter every night.

---

## Roles & Permissions

### Workspace Roles

The workspace creator is the **Admin**. They invite the first teammates and assign roles. Every member has exactly one role that determines their tools, permissions, and how the Brain interacts with them.

#### Phase 1 Roles (Core)

| Role | Description | Key Permissions |
|---|---|---|
| **Admin** | Workspace creator/owner. Full control. | All permissions, manage members, billing, Brain config, SOUL.md editing |
| **Member** | Default role. Full collaborator. | Chat, tasks, files, docs, @Brain, create channels |
| **Designer** | Visual-focused team member. | Upload assets, generate images (OpenRouter image models), manage visual files, create mood boards |
| **Marketing Coordinator** | Campaign execution and scheduling. | Create/manage campaigns, schedule posts, content calendar, email drafts, analytics access |
| **Marketing Strategist** | Ideas, planning, market positioning. | Research tools, competitive intel, brainstorm with Brain, create briefs, full web search |
| **Researcher** | Deep analysis and knowledge work. | Web search, document analysis, generate reports, full Brain research delegation |
| **Sales** | Pipeline and client relationships. | Manage contacts/leads, track deals, send proposals, email drafts, pipeline dashboard |
| **Guest** | External collaborator. Limited access. | Chat in invited channels only, no tasks, no contacts, no Brain DM |
| **Custom** | User-defined role with selected permissions. | Admin picks from permission set |

#### Professional Roles (Phase 4 — Specialization)

These roles unlock specialized Brain behaviors, custom model routing, and industry-specific skills. Each role can have a **preferred LLM** (e.g., a legal model fine-tuned for contracts) and **role-specific skills** auto-installed.

| Role | Brain Specialization | Custom Model Option | Auto-Skills |
|---|---|---|---|
| **Lawyer** | Contract analysis, clause extraction, risk flagging, precedent search | Legal fine-tuned model (e.g., legal LLM via OpenRouter) | Contract reviewer, clause library, compliance checker, deadline tracker |
| **Creative Director** | Brand voice enforcement, campaign critique, visual direction | Image models (DALL-E, SD), brand-trained model | Brand guidelines enforcer, campaign reviewer, asset organizer |
| **Accountant** | Invoice parsing, expense categorization, financial summaries | Numbers-focused model | Invoice processor, expense categorizer, monthly close checklist |
| **HR Manager** | Policy compliance, offer letter drafting, employee handbook Q&A | HR policy model | Hiring pipeline, onboarding checklist, policy Q&A bot |
| **Project Manager** | Timeline tracking, dependency detection, resource allocation | General (high reasoning) | Sprint planner, risk tracker, status report generator, RACI matrix |
| **Developer** | Code review, technical writing, architecture discussion | Code model (e.g., Claude, Codex) | Code review assistant, PR summarizer, technical doc generator |
| **Content Writer** | Tone matching, SEO optimization, editorial calendar | Writing-focused model | Editorial calendar, SEO checker, content brief generator |
| **Customer Success** | Churn signals, health scoring, renewal tracking | General | Health score tracker, churn alert, renewal reminder, NPS analyzer |
| **Executive** | Board report generation, KPI summaries, strategic analysis | High-reasoning model | Weekly KPI digest, board prep, competitive landscape, decision log |
| **Custom Professional** | Admin defines specialization | Admin selects model | Admin installs/creates skills |

**How professional roles work:**

1. Admin assigns "Lawyer" role to Sarah
2. Brain loads the Lawyer role profile — adjusts its system prompt to prioritize legal precision, cite sources, flag risks
3. When Sarah asks Brain a question, it routes to the legal-optimized model (if configured)
4. Legal skills auto-install: contract reviewer, clause library, compliance checker
5. Sarah's Brain interactions feel like talking to a legal AI assistant, while Jake (Sales) gets a sales-optimized Brain

**Role profiles are Markdown files** (like SOUL.md):
```
brain/roles/
├── lawyer.md          # Legal precision, cite statutes, flag risks
├── creative-director.md # Brand voice, visual thinking, campaign critique
├── accountant.md      # Financial accuracy, categorization, compliance
├── project-manager.md # Timeline focus, dependencies, status tracking
└── custom/            # Team-specific role definitions
```

Each role file extends the Brain's base personality with domain-specific instructions. The Brain doesn't become a different entity — it adapts its expertise based on who it's talking to.

#### AI Agent Roles (Phase 4)

Beyond human roles, specialized AI agents can be deployed as workspace members with their own identity, role, and model:

| Agent Role | What It Does | Runs As |
|---|---|---|
| **Legal Reviewer** | Reads contracts, flags clauses, tracks deadlines | Sub-agent with legal model |
| **Brand Guardian** | Reviews all outgoing content for brand voice consistency | Sub-agent watching #marketing |
| **Data Analyst** | Processes uploaded spreadsheets, generates charts and insights | Sub-agent with data model |
| **Compliance Officer** | Monitors conversations for regulatory risks | Sub-agent watching all channels |
| **Meeting Facilitator** | Runs structured meetings, tracks time, captures outcomes | Sub-agent activated per meeting |
| **Translator** | Real-time translation in multilingual teams | Sub-agent in specified channels |

These agents appear in the member list like humans — they have names, avatars, and presence. The difference: they're autonomous, always on, and run on their own model/token budget.

#### How Roles Affect the Brain

The Brain adapts its behavior based on who it's talking to:

- **Designer** asks Brain: "Create a hero image for the landing page" → Brain uses image model, suggests compositions, generates options
- **Marketing Coordinator** asks Brain: "Schedule the newsletter for Thursday" → Brain creates task, drafts email, sets calendar reminder
- **Researcher** asks Brain: "What are competitors doing in our space?" → Brain triggers deep web search, compiles report with citations
- **Sales** tells Brain: "I just talked to Acme Corp, they're interested" → Brain creates contact, starts deal tracking, drafts follow-up email
- **Lawyer** asks Brain: "Review this NDA" → Brain routes to legal model, extracts key terms, flags unusual clauses, compares to standard template
- **Executive** asks Brain: "Prepare for the board meeting" → Brain compiles KPIs, recent decisions, risks, and generates a structured brief

Roles also gate tool access:
- Only **Designer** and **Admin** can trigger image generation
- Only **Sales** and **Admin** can manage contacts
- Only **Researcher**, **Marketing Strategist**, and **Admin** get web search delegation
- **Guest** can chat but can't trigger any Brain tools

#### Permission Set

```
Permissions (granular, combined into roles):
├── chat.read           — read messages in joined channels
├── chat.write          — send messages
├── channels.create     — create new channels
├── channels.manage     — archive, rename, set classification
├── tasks.create        — create tasks
├── tasks.assign        — assign tasks to others
├── tasks.manage        — edit/delete any task
├── files.upload        — upload files
├── files.manage        — delete any file
├── contacts.view       — see contacts/CRM
├── contacts.manage     — create/edit/delete contacts
├── deals.manage        — manage sales pipeline
├── campaigns.manage    — create/manage marketing campaigns
├── brain.chat          — DM the Brain directly
├── brain.tools.search  — delegate web search to Brain
├── brain.tools.image   — trigger image generation
├── brain.tools.email   — have Brain send emails
├── brain.config        — edit Brain definition files
├── workspace.manage    — workspace settings, billing
├── members.invite      — invite new members
├── members.manage      — change roles, remove members
└── skills.create       — create custom skills
```

---

## Contacts & CRM

### Built-In Contact Management

Nexus includes a lightweight CRM — not a Salesforce replacement, but enough for small teams to track external relationships without another tool.

#### Contact

```
Contact
├── id (ULID)
├── name, email, phone, company
├── type: client | lead | vendor | partner | candidate | other
├── status: active | inactive | archived
├── owner → Identity (who manages this relationship)
├── tags[] (e.g., "enterprise", "warm-lead", "agency")
├── notes (free-form, Brain-maintained)
├── linked_channels[] (which channels discuss this contact)
├── linked_tasks[] (related tasks)
├── linked_emails[] (email threads involving this contact)
├── deal → Deal (if sales pipeline)
├── last_interaction (auto-tracked from email/chat mentions)
└── created_at, updated_at
```

#### Deal (Sales Pipeline)

```
Deal
├── id, title
├── contact → Contact
├── owner → Identity (sales rep)
├── stage: lead | qualified | proposal | negotiation | closed_won | closed_lost
├── value (amount)
├── expected_close_date
├── linked_messages[] (relevant discussions)
└── created_at, updated_at
```

#### How It Works

**Brain auto-creates contacts from email:**
> Brain is CC'd on an email from tom@acmecorp.com
> Brain: "New contact: Tom Chen, Acme Corp. Should I add them as a client or lead?"

**Brain tracks interactions:**
> Brain: "No one has contacted Acme Corp in 14 days. Sarah, should I draft a check-in?"

**Pipeline view in UI:**
```
┌─ Sales Pipeline ─────────────────────────────────────────┐
│                                                           │
│  Lead (3)        Qualified (2)    Proposal (1)   Won (4)  │
│  ┌──────────┐   ┌──────────┐    ┌──────────┐            │
│  │ Acme Corp│   │ Beta Inc │    │ Gamma LLC│            │
│  │ $12,000  │   │ $8,500   │    │ $25,000  │            │
│  │ Sarah    │   │ Jake     │    │ Sarah    │            │
│  └──────────┘   └──────────┘    └──────────┘            │
│  ┌──────────┐   ┌──────────┐                             │
│  │ Delta Co │   │ Echo Ltd │                             │
│  │ $5,000   │   │ $15,000  │                             │
│  └──────────┘   └──────────┘                             │
└───────────────────────────────────────────────────────────┘
```

**Contact card in sidebar:**
```
┌─ Tom Chen ─────────────────────┐
│  Acme Corp · Client             │
│  tom@acmecorp.com              │
│  Owner: Sarah                   │
│                                 │
│  Deal: $12,000 · Lead stage     │
│  Last contact: 3 days ago       │
│                                 │
│  Channels: #client-acme         │
│  Tasks: 2 open                  │
│  Emails: 5 threads              │
│                                 │
│  Brain notes:                   │
│  "Interested in premium tier.   │
│   Mentioned budget approval     │
│   needed from their CFO."       │
└─────────────────────────────────┘
```

---

## Team Skills

### What Are Team Skills

Skills are **collaborative workflows** — not personal automation scripts. They help groups of people work together. Each skill is a SKILL.md that the Brain reads as instructions.

### Skill Categories

#### Rhythm — Keep the Team Moving

**Daily Standup**
```yaml
---
name: daily-standup
description: Runs async standup every morning
schedule: "weekdays 9:00am"
channels: ["#general"]
autonomy: autonomous
---
Every morning, post in #general asking each team member:
1. What did you do yesterday?
2. What are you doing today?
3. Any blockers?

Collect responses throughout the morning.
At 11am, compile a summary and post it.
Track who didn't respond — gentle nudge at 10:30am.
```

**Weekly Retro**
```yaml
---
name: weekly-retro
description: Facilitates Friday retrospective
schedule: "friday 4:00pm"
channels: ["#general"]
---
Post three prompts:
1. What went well this week?
2. What didn't go well?
3. What should we change?

Collect responses for 1 hour. Summarize themes.
Create action items as tasks, assigned to volunteers.
```

**Meeting Notes**
```yaml
---
name: meeting-notes
description: Captures decisions and action items from any conversation
channels: ["*"]
autonomy: suggest
---
When a conversation involves 3+ people and runs longer than 10 messages,
offer to capture: decisions made, action items, open questions.
Create tasks for action items. Pin decisions.
```

#### Client-Facing — Manage External Relationships

**Client Onboarding**
```yaml
---
name: client-onboarding
description: Manages new client setup
trigger: "task created with tag 'new-client'"
roles: [sales, admin]
---
When a new client is tagged:
1. Create a private channel #client-{name}
2. Add assigned team members
3. Post welcome template with key info needed
4. Create onboarding checklist as tasks
5. Send welcome email to client (draft for approval)
6. Schedule kickoff meeting (.ics)
7. Set 30-day check-in reminder
8. Create contact in CRM if not exists
```

**Proposal Tracker**
```yaml
---
name: proposal-tracker
description: Tracks outbound proposals and follows up
channels: ["#sales"]
roles: [sales]
---
When someone mentions sending a proposal:
1. Create a task with the client name, amount, deadline
2. Track the email thread (if CC'd)
3. If no response in 3 business days, suggest follow-up
4. If accepted, trigger client-onboarding skill
5. Weekly: post pipeline summary in #sales
6. Update deal stage in CRM
```

**Support Triage**
```yaml
---
name: support-triage
description: Routes and prioritizes incoming support
channels: ["#support"]
autonomy: autonomous
---
When a customer message arrives (email or channel):
1. Categorize: bug, feature request, billing, general question
2. Check knowledge graph for known issues
3. If known issue: draft response with status update
4. If new: create task, assign to on-call person
5. Respond to customer with acknowledgment and ETA
6. Escalate if customer mentions "urgent" or "cancel"
7. Update contact record with interaction
```

#### Operations — Run the Business

**Invoice Reminder**
```yaml
---
name: invoice-reminder
description: Tracks payment deadlines
schedule: "weekdays 9:00am"
channels: ["#finance"]
---
Check tasks tagged 'invoice' for approaching due dates.
3 days before: remind the team in #finance.
On due date: draft follow-up email to client.
Overdue: escalate to manager, suggest follow-up cadence.
Update contact record with payment status.
```

**Hiring Pipeline**
```yaml
---
name: hiring-pipeline
description: Tracks candidates through interview process
channels: ["#hiring"]
---
When a candidate is mentioned:
1. Create contact (type: candidate) if not exists
2. Track stages: applied → screened → interviewed → offer → hired
3. Remind interviewers to submit feedback within 24h
4. Weekly: post pipeline summary (X candidates, Y interviews this week)
5. Flag stale candidates (no activity in 5+ days)
```

**Expense Tracking**
```yaml
---
name: expense-tracker
description: Logs team expenses from receipts
channels: ["#expenses"]
---
When someone uploads a receipt or mentions an expense:
1. Extract: amount, vendor, date, category
2. Log it as a structured record
3. Running total for the month in #expenses
4. Monthly summary on the 1st
```

#### Knowledge — Make the Team Smarter

**Competitive Intel**
```yaml
---
name: competitive-intel
description: Monitors and summarizes competitor activity
schedule: "weekly monday 9:00am"
tools: [web_search]
roles: [marketing_strategist, researcher, admin]
---
Every Monday, search for news about [configured competitors].
Post a digest in #strategy:
- Product launches, pricing changes
- Hiring patterns (what roles they're posting)
- Funding news
Flag anything that affects our positioning.
```

**New Hire Buddy**
```yaml
---
name: new-hire-buddy
description: Onboards new team members
trigger: "new member joins workspace"
autonomy: autonomous
---
When someone new joins:
1. DM them a welcome message with team overview
2. Introduce them in #general with their role
3. Share key decisions from the last 2 weeks
4. Point them to important pinned artifacts
5. Check in after day 1, day 3, and week 1
6. Answer any questions about "how we do things here"
```

**Decision Logger**
```yaml
---
name: decision-logger
description: Captures and indexes team decisions
channels: ["*"]
autonomy: suggest
---
When a conversation reaches consensus or someone says "let's do X":
1. Confirm: "It sounds like you've decided [X]. Should I log this?"
2. If yes: create Decision artifact with participants and context
3. Link to the message thread
4. Make it searchable: "What did we decide about [topic]?"
```

#### Marketing — Campaign & Content

**Content Calendar**
```yaml
---
name: content-calendar
description: Manages content publishing schedule
channels: ["#marketing"]
roles: [marketing_coordinator, marketing_strategist]
---
Maintain a content calendar from tasks tagged 'content'.
Weekly reminder: what's due for publishing this week.
Track drafts → review → published stages.
Nudge authors 2 days before deadline.
Post "published this week" summary on Fridays.
```

**Campaign Manager**
```yaml
---
name: campaign-manager
description: Plans and tracks marketing campaigns
channels: ["#marketing"]
roles: [marketing_coordinator, marketing_strategist, admin]
---
When a campaign is created:
1. Create a private channel #campaign-{name}
2. Generate task checklist: goals, audience, channels, assets, timeline
3. Assign tasks to team members by role
4. Track asset creation progress (copy, visuals, landing pages)
5. Daily brief during active campaign
6. Post-campaign: compile results summary
```

**Release Notes**
```yaml
---
name: release-notes
description: Compiles release notes from completed tasks
trigger: "manual or scheduled"
---
When triggered:
1. Collect all tasks marked 'done' since last release
2. Categorize: features, fixes, improvements
3. Draft release notes in user-friendly language
4. Post draft in #product for review
5. After approval, format for email/blog
```

### Skill vs OpenClaw Skills

| | OpenClaw Skills | Nexus Skills |
|---|---|---|
| Scope | Personal automation | Team workflows |
| Trigger | User command | Schedule, events, conversation patterns |
| Output | Action on your machine | Messages, tasks, emails, contacts, artifacts |
| State | Your local files | Shared knowledge graph + CRM |
| Users | Just you | Whole team sees and benefits |
| Roles | N/A (single user) | Role-gated (only sales can trigger deal skills) |
| Examples | "Open browser", "Check calendar" | "Run standup", "Onboard client", "Track campaign" |

### Skill Library (Planned Marketplace)

Teams share what works. Templates organized by function:

```
Nexus Skill Library
├── Team Rhythm (standup, retro, meeting notes, daily brief)
├── Client Management (onboarding, proposals, support triage)
├── Sales (deal tracking, follow-ups, pipeline reports)
├── Marketing (campaigns, content calendar, competitive intel)
├── Operations (invoicing, hiring, expenses)
├── Knowledge (decision log, new hire buddy, research)
└── Custom (your team's unique workflows)
```

Copy a skill to your workspace, customize the SKILL.md, done.

---

## Data Model

```
Identity (shared by humans and agents)
├── id (ULID — sortable, unique, no coordination needed)
├── display_name, avatar_url
├── type: human | brain | sub_agent
├── workspace_role: admin | member | designer | marketing_coordinator
│                   | marketing_strategist | researcher | sales | guest | custom
├── permissions[] (derived from role, overridable by admin)
├── preferences{}
└── status: online | busy | focus | away | offline

Workspace
├── id (short random slug, e.g. "x7k9m2")
├── name, description
├── brain → Identity (the workspace's Brain)
├── channels[]
├── members[] → Identity (with roles)
├── contacts[] → Contact
├── settings (LLM config, attention level, default roles, etc.)
└── created_at, owner → Identity (admin)

Channel
├── id, name, type: public | private | dm
├── messages[]
├── pinned_artifacts[] (tasks, decisions, files)
├── working_memory (Brain's rolling context for this channel)
├── subscribed_agents[] → Identity
└── classification: public | internal | confidential | restricted

Message
├── id (ULID)
├── author → Identity (human or agent)
├── content (text, markdown)
├── artifacts[] (typed structured objects)
├── encrypted_payload (nullable — for future E2E)
├── classification (inherits from channel or overridden)
└── created_at, edited_at

Task
├── id, title, description
├── assignee → Identity (human OR agent)
├── creator → Identity
├── status: backlog | todo | in_progress | done | cancelled
├── priority: low | medium | high | urgent
├── due_date, completed_at
├── tags[] (e.g., "content", "invoice", "new-client")
├── linked_messages[] (where it was discussed)
├── linked_decisions[] (why it exists)
├── linked_contacts[] → Contact
└── channel → Channel (home channel)

Contact
├── id (ULID)
├── name, email, phone, company
├── type: client | lead | vendor | partner | candidate | other
├── status: active | inactive | archived
├── owner → Identity (who manages this relationship)
├── tags[]
├── notes (free-form, Brain-maintained)
├── linked_channels[]
├── linked_tasks[]
├── linked_emails[]
├── deal → Deal (nullable)
├── last_interaction (auto-tracked)
└── created_at, updated_at

Deal
├── id, title
├── contact → Contact
├── owner → Identity
├── stage: lead | qualified | proposal | negotiation | closed_won | closed_lost
├── value (amount)
├── expected_close_date
├── linked_messages[]
└── created_at, updated_at

Decision
├── id, summary
├── decided_by → Identity
├── participants[] → Identity
├── context (link to message thread)
├── status: proposed | decided | revisited | reversed
└── decided_at

Document
├── id (ULID)
├── title
├── content (Tiptap JSON — block-based)
├── author → Identity
├── collaborators[] → Identity
├── shared_with: workspace | channel | private
├── tags[]
├── linked_tasks[] → Task
├── linked_contacts[] → Contact
├── linked_channels[] → Channel
├── template (nullable)
├── word_count, last_edited_by → Identity
└── created_at, updated_at

Artifact (union type embedded in messages)
├── type: task | decision | calendar_event | file_ref | doc_ref | contact_ref | deal_ref
└── data: (type-specific structured payload)

AgentConfig (for Brain and sub-agents)
├── identity → Identity
├── agent_role: brain | scribe | researcher | coordinator | taskmaster | custom
├── model_preference (OpenRouter model ID or "local")
├── skill_files[] (paths to SKILL.md files this agent runs)
├── tool_access[] (tasks, contacts, calendar, files, web_search, email, channels)
├── attention_channels[] → Channel
├── attention_level: reactive | moderate | proactive
└── token_budget (monthly limit, nullable)

File
├── id, name, mime_type, size_bytes
├── hash (SHA-256, content-addressed)
├── uploaded_by → Identity
├── workspace → Workspace
└── created_at
```

---

## Directory Structure

```
/var/nexus/                         # --data-dir
├── nexus.db                        # Global DB (accounts, workspace index)
├── config.toml                     # Server config (optional, CLI flags override)
├── workspaces/
│   ├── x7k9m2/
│   │   ├── workspace.db            # Workspace SQLite (messages, tasks, memory layers)
│   │   ├── workspace.db-wal
│   │   ├── brain/                  # Brain definition (Markdown files)
│   │   │   ├── SOUL.md             # Persona, tone, boundaries
│   │   │   ├── INSTRUCTIONS.md     # Workspace-specific operating rules
│   │   │   ├── TEAM.md             # Team profiles (Brain-maintained)
│   │   │   ├── MEMORY.md           # Long-term curated knowledge
│   │   │   ├── HEARTBEAT.md        # Scheduled routine definitions
│   │   │   └── skills/             # Modular skill definitions
│   │   │       ├── support-drafter/SKILL.md
│   │   │       └── deal-tracker/SKILL.md
│   │   └── blobs/                  # Content-addressed file storage
│   │       └── ab/ab3f7c8d...
│   └── k3p8n1/
│       ├── workspace.db
│       ├── brain/
│       └── blobs/
├── models/                         # Local LLM models (optional)
│   └── qwen-7b-q4.gguf
└── backups/                        # Automatic daily SQLite backups
```

### Per-Workspace SQLite Isolation

Each workspace is its own SQLite database file. Benefits:
- **Natural multi-tenancy** — one workspace can't accidentally access another's data
- **Easy backup/restore** — copy one file to back up a workspace
- **Easy deletion** — `rm -rf workspaces/x7k9m2/` and it's gone
- **Easy migration** — move a workspace to a different server by copying its directory
- **Performance** — WAL mode per workspace, no cross-workspace contention

---

## CLI Commands

```
nexus serve                        # Start the server
nexus serve --domain nexus.app     # With auto-TLS
nexus serve --llama-model ./q.gguf # With local LLM
nexus migrate                      # Run database migrations
nexus adduser                      # Create admin user from CLI
nexus backup                       # Manual backup of all workspaces
nexus backup --workspace x7k9m2    # Backup specific workspace
nexus version                      # Print version + build info
```

First run auto-initializes and prompts for admin setup:
```
./nexus serve --admin-email admin@company.com --admin-pass changeme
```

---

## Security Architecture

### TLS
Built-in autocert via Let's Encrypt:
```
./nexus serve --domain chat.company.com --autocert
```

### Auth
- Phase 1: Anonymous (instant workspaces) + optional email/password (bcrypt) for persistence
- Phase 2: OIDC/SAML for enterprise SSO (Okta, Google Workspace, Azure AD)
- Agents authenticate the same way as users — they just have type `brain` or `sub_agent`

### Message Classification
Every channel and message has a classification level:
- `public` — can be processed by any model, including external
- `internal` — external models OK, but not shared outside workspace
- `confidential` — local LLM only, never sent to OpenRouter
- `restricted` — local LLM only, ephemeral, auto-deletes

Agents respect classification: a `confidential` message is never sent to OpenRouter, only processed by local llama.cpp. If no local model is available, the Brain says so instead of silently leaking data.

### E2E Encryption Path (Future)
- Message schema has `encrypted_payload` field from day one (nullable)
- Add MLS (RFC 9420) based E2E without changing the protocol
- Brain gets its own MLS group membership — users control which channels it can read
- If a channel doesn't add the Brain, it literally cannot decrypt those messages

### Planned Security Features
- **Zero-Knowledge Architecture** — server as dumb relay + encrypted blob store
- **Ephemeral Channels** — auto-delete after N hours/days with cryptographic proof of deletion
- **BYOK (Bring Your Own Key)** — each team manages their own encryption keys
- **Compliance Export** — designated officer can decrypt for legal holds while E2E is maintained for everyone else
- **Air-Gapped Mode** — runs entirely without internet, LLM via llama.cpp
- **Auditable Crypto** — open-source the encryption layer for independent verification

---

## Documents & Notes (Tiptap Block Editor)

### Notion-Style Documents, Built Into the Platform

Nexus includes a block-based document editor for team notes, wikis, meeting notes, project briefs, and any long-form content. Documents are first-class workspace content alongside chat, tasks, and contacts.

### Tech: Tiptap + Svelte

- **Tiptap** — headless rich text editor framework built on ProseMirror (~35k GitHub stars, MIT license)
- **Official Svelte support** — `svelte-tiptap` bindings + Tipex SvelteKit wrapper
- **Yjs collaboration** — real-time multi-user editing via WebSocket (CRDT-based)
- **JSON document model** — Brain can read and write documents programmatically
- **Block types:** paragraphs, headings, lists, checklists, code blocks, images, tables, embeds, callouts, dividers
- **Slash menu** — type `/` to insert any block type (Notion-style)
- **Inline mentions** — @person, #channel, [[document]], link to tasks and contacts

### How Documents Fit Into Nexus

**Linked to everything:**
- Documents can be linked from chat messages, tasks, contacts, and decisions
- "See the project brief" in chat → clickable link opens the document
- Tasks can have linked docs (spec, design brief, meeting notes)
- Contacts can have linked docs (proposals, contracts, SOWs)

**Brain can read and write docs:**
- Brain reads docs as part of its knowledge graph
- "Brain, summarize the project brief" → Brain reads the Tiptap JSON, responds
- "Brain, create a meeting notes doc for today's standup" → Brain creates and populates
- Brain can update docs autonomously (e.g., append action items after a conversation)
- Skills can reference docs (e.g., "Client Onboarding" skill creates a welcome doc from template)

**Real-time collaboration:**
- Multiple team members edit the same document simultaneously
- Cursors and selections visible in real-time (like Google Docs)
- Yjs CRDT handles conflict resolution — no data loss on concurrent edits
- Changes sync through the Go backend via WebSocket

**Document templates:**
- Meeting notes, project brief, client proposal, weekly report, product spec
- Brain can generate docs from templates and fill them with context
- Skills can specify templates in their SKILL.md

### Architecture

```
SvelteKit Frontend
  → Tiptap editor (via svelte-tiptap)
  → Custom block UI components (Svelte)
  → Yjs provider (WebSocket to Go backend)

Go Backend
  → Yjs WebSocket sync server
  → Document storage in workspace SQLite (JSON)
  → Tiptap JSON indexed for Brain knowledge graph
  → Document API (create, list, search, share)
```

### Data Model Addition

```
Document
├── id (ULID)
├── title
├── content (Tiptap JSON — block-based)
├── author → Identity
├── collaborators[] → Identity (who has edit access)
├── shared_with: workspace | channel | private
├── tags[]
├── linked_tasks[] → Task
├── linked_contacts[] → Contact
├── linked_channels[] → Channel
├── template (nullable — which template this was created from)
├── word_count, last_edited_by → Identity
└── created_at, updated_at
```

---

## Structured Artifacts

Messages can contain typed artifacts (not just text):
- **Tasks** — assignee, status, priority, due date
- **Decisions** — what was decided, by whom, context
- **Calendar Events** — proposals that map to device calendar
- **File References** — uploaded documents with inline preview
- **Document References** — links to workspace documents

These are structured objects that:
- Brain and sub-agents can create, modify, and track
- Get indexed separately from chat
- Feed into dashboards (task board, calendar overlay, decision log, document library)
- Auto-parsed from natural language ("let's meet Thursday at 3" → calendar proposal)
- Have their own query API (list all open tasks, show recent decisions, search documents, etc.)

---

## Integrations — Making the Brain Omniscient

### Strategy: Layers, Not Bespoke Integrations

Instead of building dozens of custom integrations, Nexus uses a layered approach — each tier is more powerful but more complex:

```
Tier 1 (Phase 1):  Webhook ingestion — universal, zero effort
Tier 2 (Phase 1):  Email ingestion — every team's primary external data flow
Tier 3 (Phase 1):  Brain-as-coordinator — no API needed, Brain talks to humans
Tier 4 (Phase 2):  MCP client — hundreds of tools, community-built
Tier 5 (Later):    OAuth integrations — Google, Microsoft, etc. for deep two-way access
```

---

### Tier 1: Webhook Ingestion (Universal)

Every workspace gets a webhook URL:

```
https://nexus.app/w/x7k9m2/hook/abc123
```

Any app that can fire a webhook sends events to the Brain. Zapier, Make, n8n, IFTTT, or direct from apps (GitHub, Stripe, deploy pipelines, monitoring, CRMs, etc.).

Brain doesn't need to understand every app's API. It receives structured events and uses LLM to interpret them in context:

```json
POST /hook/abc123
{
  "source": "stripe",
  "event": "payment.received",
  "data": { "amount": 4999, "customer": "Acme Corp" }
}
```

> **Brain:** New payment from Acme Corp ($49.99). This is the deal Sarah was tracking in #sales — marking it as closed-won.

**Complexity:** Very low. It's just an HTTP endpoint + LLM interpretation.

---

### Tier 2: Email — The Brain as an Active Participant

Email is the universal work protocol. Every team, every industry, every company. The Brain doesn't just ingest emails — it's a **full email participant**. CC the Brain on anything and it reads, responds, and acts.

**Every workspace gets a Brain email address:**

```
brain@company.com          # Self-hosted with custom domain
brain-x7k9m2@nexus.app    # Hosted
```

**The core interaction: CC the Brain on any email, it handles the rest.**

---

**Example 1: Client schedules a meeting**
> Client emails Sarah, CC brain@company.com:
> "Can we meet Thursday to review the proposal?"
>
> Brain replies-all:
> "Hi Tom, Thursday works. I've checked with the team — 2:30pm is open for everyone. Here's a calendar invite."
> *(attaches .ics file)*
>
> Brain also: creates a task "Prepare proposal review for Acme" assigned to Sarah, posts in #clients

**Example 2: Vendor sends a contract**
> Legal team CC's Brain on a vendor contract thread.
>
> Brain replies to the team (not the vendor):
> "I've read the contract. Key terms: 24-month commitment, $4,200/mo, 90-day termination notice. There's a non-compete clause in section 7 that's broader than what we agreed verbally. Flagging for review."
>
> Brain also: indexes contract in knowledge graph, creates task "Review vendor non-compete clause" assigned to legal lead

**Example 3: Support email comes in**
> Customer emails support@company.com (which Brain monitors):
>
> Brain replies to the customer:
> "Hi, thanks for reaching out. I see you're having trouble with exports — our team is already working on a fix. I'll follow up when it's resolved."
>
> Brain also: links to the existing bug in #engineering, posts in #support, creates task if none exists

**Example 4: Someone CC's Brain on an FYI**
> Jake CC's Brain on a newsletter about a competitor launch.
>
> Brain doesn't reply to the email (nothing to respond to).
>
> Brain posts in #general: "Heads up — competitor X just launched feature Y. This overlaps with what Sarah proposed last week in #product. Summary: [key points]"

---

**The Brain decides what to do based on context:**

| Email type | Brain replies? | Brain acts? |
|---|---|---|
| Meeting request | Yes — proposes times, sends .ics | Creates prep task, posts in channel |
| Client question | Yes — answers or acknowledges | Routes to right channel, creates task if needed |
| Contract/document | No — responds internally only | Indexes, extracts key terms, flags issues |
| FYI/newsletter | No | Summarizes, posts in relevant channel |
| Support request | Yes — acknowledges, sets expectations | Creates/links tasks, routes to team |
| Intro/handoff | Yes — confirms receipt | Creates contact in knowledge graph |

**Autonomy controls per workspace:**

```
Email Response:     [Autonomous] [Draft + Approve] [Never]
  └── Autonomous:   Brain replies directly when confident
  └── Draft:        Brain drafts, posts in channel, human clicks Send
  └── Never:        Brain only takes internal actions, never replies

Who can Brain email:
  └── [Anyone]  [Only known contacts]  [Only internal]

Action autonomy:
  └── [Full]    Brain creates tasks, posts, updates knowledge graph
  └── [Propose] Brain suggests actions, human confirms
```

**Outbound SMTP setup:**
- **Hosted (nexus.app):** provided — `workspace-name@nexus.app` or custom domain
- **Self-hosted:** admin configures SMTP (Gmail, Outlook, SendGrid, company mail server)

```
Settings → Email
  Inbound:  brain-x7k9m2@nexus.app (always on)
  Outbound: smtp.company.com:587 / brain@company.com
  Reply behavior: Autonomous
  Action autonomy: Full
```

**Complexity:** Medium. Inbound SMTP + outbound SMTP + LLM-driven decision engine for when/how to respond. But the payoff is massive — the Brain becomes a team member that handles email.

---

### Tier 3: MCP — Model Context Protocol (Phase 2)

Anthropic's open standard for tool access. Growing fast.

```
Brain ←→ MCP ←→ Any MCP-compatible tool
```

MCP servers already exist for: Google Calendar, Google Drive, Slack, Notion, Postgres, filesystem, and dozens more. Community builds new ones weekly.

**What this means for Nexus:**
- We don't build most integrations — we host MCP connections
- User points Brain at any MCP server, Brain auto-discovers available tools
- New tool comes out with MCP support? Brain works with it immediately
- Self-hosted users write their own MCP servers for internal tools

**Calendar, Drive, and everything else — via MCP:**
- Google Calendar MCP → Brain reads/writes events directly
- Google Drive MCP → Brain indexes and searches documents
- Notion MCP → Brain reads/writes wiki pages
- Any new MCP server → Brain uses it immediately

This is where calendar gets real API access. Until MCP is wired up, the Brain handles scheduling socially (asks people for availability, generates .ics files) and via email (replies to meeting requests with calendar attachments). Once MCP is connected, the Brain can check availability and create events directly.

**Setup:**
```
Settings → MCP Servers → Add
  Name: Google Calendar
  Command: npx @anthropic/mcp-google-calendar
  [Save]

  Name: Google Drive
  Command: npx @anthropic/mcp-google-drive
  [Save]
```

**Complexity:** Medium to implement the MCP client in Go, but then every future integration comes free.

---

### Tier 5: OAuth Integrations (Later)

Full two-way integrations for apps that warrant deep access:

**Google Workspace** (Calendar, Drive, Gmail)
- Read/write calendar events directly
- Index Drive documents into Brain's knowledge graph
- Monitor shared inboxes, surface relevant emails

**Microsoft 365** (Outlook, OneDrive, Teams)
- Same as Google but for Microsoft shops

**CRM (HubSpot, Salesforce)**
- Brain knows about deals, contacts, pipeline
- Sales intelligence in Nexus channels

These require OAuth flows, cloud project setup, and more maintenance. Worth it for specific high-value integrations, but not needed early.

---

### Integration UI

```
┌─ Integrations ──────────────────────────────────────┐
│                                                      │
│  Webhook URL                                         │
│  https://nexus.app/w/x7k9m2/hook/abc123  [Copy]     │
│  Send events from any app — Brain interprets them    │
│                                                      │
│                                                      │
│  MCP Servers                                         │
│  ├── Google Drive    ✅ Running (12 tools available)  │
│  ├── Notion          ✅ Running (8 tools available)   │
│  └── + Add MCP Server                                │
│                                                      │
│  OAuth (Coming Soon)                                 │
│  ├── Google Workspace                                │
│  └── Microsoft 365                                   │
│                                                      │
└──────────────────────────────────────────────────────┘
```

---

## Platform Considerations (iOS / Mac)

While Phase 1 is web-only, the architecture is designed for native clients:

- **WebSocket protocol is platform-agnostic** — same JSON messages for web, iOS, Mac
- **SvelteKit's reactive model** maps well to SwiftUI's paradigm
- **Per-workspace SQLite** can be synced to devices for offline support (CRDT or simple sync)
- **Device calendar integration** via EventKit (iOS/Mac) or CalDAV (web)
- **Push notifications** via APNs when native clients exist
- **Brain runs server-side** — native clients are thin, just UI + WebSocket connection

---

## Platform Roadmap

> Detailed implementation plan with task-level breakdown: see [PLAN.md](PLAN.md)

Every phase delivers a usable product improvement. Every phase advances privacy. Innovation is not saved for later — each phase introduces something no competitor has.

---

### Phase 1 — The Living Workspace [~15 weeks]

**What ships:** A complete team platform — chat, Brain, tasks, docs, contacts — usable from a browser.

**The innovation:** The Brain is there from second one. You don't set up a workspace and then "add AI." You walk into a room where the Brain is already waiting, ready to work. No other product does this.

1. Go server binary — `nexus serve`, auto-TLS, SQLite, per-workspace DB isolation
2. Instant workspaces — zero-signup, unique URL, shareable invite links
3. Web UI (SvelteKit) — channels, real-time messaging, DMs, responsive
4. Auth — anonymous sessions + optional email/password, JWT tokens
5. Roles & permissions — admin (creator), member, designer, marketing, researcher, sales, guest
6. The Brain — present in every workspace, OpenRouter, role-aware responses
7. Brain definition files — SOUL.md, INSTRUCTIONS.md, TEAM.md, MEMORY.md, HEARTBEAT.md
8. Brain memory system — 5-layer hierarchy, atomic extraction, knowledge graph, auto-flush
9. Tasks — board view, tags, linked to messages/contacts/decisions/documents
10. Documents & Notes — Tiptap block editor, Yjs real-time collaboration, Brain read/write
11. Contacts & CRM — contact types, deal pipeline, auto-created from email
12. File sharing — content-addressed storage, inline preview
13. Heartbeat scheduler — morning briefs, overdue nudges, weekly summaries
14. Core skills — daily standup, meeting notes, decision logger, new hire buddy
15. Observability — Brain action log, audit trail, token usage tracking

**Privacy in Phase 1:**
- Per-workspace SQLite isolation — one workspace physically cannot access another's data
- All data on your server (self-hosted) or isolated per workspace (cloud)
- Message classification field from day one (stored, not enforced yet — schema-ready for E2E)
- `encrypted_payload` field on all messages (nullable — ready for Phase 5)
- No telemetry, no analytics, no data mining — the server is dumb storage + Brain
- Anonymous access option — no email required, no tracking

**Key milestones:**
- Week 4: usable chat platform (no AI yet)
- Week 8: chat + tasks + docs + files (full collaboration platform)
- Week 10: Brain is live and responding
- Week 15: full Phase 1, ready for beta users

---

### Phase 2 — The Brain Reaches Out [~8 weeks]

**What ships:** The Brain communicates beyond the web UI — email, Telegram, webhooks. It becomes the team's communication hub, not just a chat feature.

**The innovation:** CC the Brain on any email and it handles the rest — responds, creates tasks, updates CRM, routes to the right channel. No setup, no integration. Just CC.

1. Email — Brain email address per workspace, CC on anything, Brain responds + acts
2. Email autonomy controls — autonomous / draft+approve / never, per workspace
3. Telegram bridge — Bot API, Brain reachable from Telegram, same memory/personality
4. Webhook ingestion — universal endpoint, Brain interprets any JSON event
5. Social scheduling — .ics generation, calendar deep links, Brain coordinates via chat + email
6. Auto-create contacts from email senders
7. Channel adapter framework — normalize → route → format (foundation for more bridges)

**Privacy in Phase 2:**
- Email stored on your server, never forwarded to third parties
- Telegram messages routed through your server, not stored by Telegram beyond delivery
- Webhook payloads processed and discarded (or stored per policy)
- Brain email replies can be restricted: only known contacts, only internal, or anyone

---

### Phase 3 — Deep Intelligence [~10 weeks]

**What ships:** Full skills system, sub-agents, think-while-idle, document ingestion. The Brain transforms from a smart responder into a proactive team member that anticipates needs.

**The innovation:** Think-while-idle. The Brain processes the day's conversations overnight, consolidates knowledge, identifies gaps, and generates tomorrow's priorities — before anyone logs in. No other team tool does this.

1. Full skills system — SKILL.md + natural language creation ("@Brain create an agent that...")
2. Advanced skills — client onboarding, proposal tracker, support triage, campaign manager, content calendar, competitive intel, invoice reminder, hiring pipeline
3. Sub-agent runtime — bounded communication (max 5 rounds), isolated sessions, provenance
4. Self-critique — Brain reviews its own output before autonomous actions
5. Think while idle — overnight consolidation, morning synthesis, gap identification, meeting prep
6. Document ingestion — PDF/DOCX/XLSX text extraction, embeddings, semantic search (sqlite-vec)
7. Cost dashboard — per-channel, per-skill, per-user token spend with budget alerts
8. MCP client — connect Brain to any MCP server (Calendar, Drive, Notion, etc.)
9. More channel bridges — WhatsApp, Slack (migration path from Slack to Nexus)
10. Custom roles — admin creates roles with selected permissions

**Privacy in Phase 3:**
- Skills run server-side — skill logic never leaves your infrastructure
- Document embeddings stored locally in SQLite (sqlite-vec), not sent to external vector DBs
- MCP servers run on your server — data flows MCP↔Brain, never to Nexus cloud
- Token budget controls — prevent runaway LLM costs from autonomous skills
- Sub-agents are sandboxed — they only access tools defined in their SKILL.md

---

### Phase 4 — Specialization [~8 weeks]

**What ships:** Professional roles with custom models, AI agent team members, and industry-specific Brain adaptations. The Brain stops being generic and starts being expert.

**The innovation:** Role-based model routing. When a Lawyer asks the Brain to review a contract, it routes to a legal-optimized model. When a Designer asks for a hero image, it routes to an image model. Same Brain, different expertise per person. No other platform adapts AI this way.

1. Professional roles — lawyer, creative director, accountant, HR manager, project manager, developer, content writer, customer success, executive
2. Role profiles as Markdown — `brain/roles/lawyer.md` extends Brain personality per role
3. Per-role model routing — route to specialized models via OpenRouter based on who's asking
4. Role-specific auto-skills — installing a role auto-installs relevant skills
5. AI agent roles — deploy autonomous agents as workspace members (Legal Reviewer, Brand Guardian, Data Analyst, Compliance Officer, Translator)
6. Agent identity — agents have names, avatars, presence, their own token budget
7. llama.cpp integration — local/private inference, run models on your server
8. Model management — configure which models are available, set defaults per role

**Privacy in Phase 4:**
- llama.cpp means zero tokens leave your server — full air-gap capability
- Role-based model routing can enforce: "legal queries NEVER go to external models"
- Confidential channel classification enforced — local LLM only, no OpenRouter
- AI agents run on your infrastructure, not in Nexus cloud
- Model selection is per-workspace — you control which providers see your data

---

### Phase 5 — Fortress [~8 weeks]

**What ships:** End-to-end encryption, SSO, compliance features. Nexus becomes deployable in regulated industries — healthcare, legal, finance, government.

**The innovation:** E2E encryption where the Brain is a controlled participant. Users explicitly grant the Brain access to channels — if they don't, the Brain literally cannot decrypt those messages. No other AI platform gives users this level of cryptographic control over what the AI can see.

1. E2E encryption — MLS-based (RFC 9420), Brain gets its own group membership
2. Message classification enforcement — confidential = local LLM only, restricted = ephemeral + local only
3. OIDC/SAML SSO — Okta, Google Workspace, Azure AD
4. Ephemeral channels — auto-delete with cryptographic proof of deletion
5. Compliance export — designated officer can decrypt for legal holds
6. Audit logging — all admin actions, Brain actions, permission changes, data access
7. BYOK (Bring Your Own Key) — each workspace manages its own encryption keys
8. IP allowlisting, two-factor authentication
9. Security audit command — `nexus security audit` flags misconfigurations

**Privacy in Phase 5:**
- This IS the privacy phase — everything here is about locking down data
- Zero-knowledge option: server stores only ciphertext it cannot read
- Brain's access to channels is cryptographically controlled, not just permission-based
- Key revocation = instant data inaccessibility (even for the server operator)
- Compliance and privacy coexist: legal holds don't break E2E for non-officers

---

### Phase 6 — Everywhere [~12 weeks]

**What ships:** Native clients (iOS, Mac), offline support, and ecosystem expansion. Nexus works without internet, on any device, integrated with any tool.

**The innovation:** Offline-first with CRDT sync. Work on a plane, in a bunker, in a country with unreliable internet. Everything syncs when you reconnect — no data loss, no conflicts. The Brain catches up and processes what happened while offline.

1. iOS app — Swift, WebSocket protocol, push notifications (APNs)
2. Mac app — optional native wrapper, menu bar presence
3. Device calendar integration — EventKit read/write (propose → confirm → write)
4. Offline support — CRDT sync (Yjs for docs, custom for messages/tasks)
5. Local LLM on Apple Silicon — llama.cpp in native apps
6. OAuth integrations — Google Workspace, Microsoft 365
7. Skill marketplace — community-contributed skills, searchable, one-click install
8. Developer API — external apps can interact with Nexus workspaces
9. Multi-server federation — optional, for large organizations
10. White-label / custom branding

**Privacy in Phase 6:**
- Local LLM on device — queries never leave the phone/laptop
- CRDT sync means the server is just a relay — clients hold the truth
- Offline mode = fully private by definition (no network, no data leak)
- Federation uses E2E between servers — inter-server traffic is encrypted
- Native apps use platform keychain for credential storage

---

## Competitive Landscape & Inspiration

### The Market Gap

Every existing product falls into one of two buckets:
1. **Agent builders** (CrewAI, Lindy, Relevance AI, Zapier) — you build agents that do tasks, but they live outside your communication layer
2. **Knowledge platforms** (Dust, Glean) — agents that know your company, but they're a separate app

**No one has built the product where the AI IS the communication layer.** Slack added AI as a feature. Dust is a separate tool. Nexus is the first where the Brain isn't bolted on — it IS the central nervous system of team collaboration.

### Key Products & What to Learn

**Dust.tt** — Closest competitor. 80,000 agents created, 12M conversations, mostly by non-engineers (PMs, ops leads, domain experts). Proves teams will build thousands of specialized agents if UX is right. Key insight: "agent as team infrastructure" not "personal assistant." **Gap Nexus fills:** Dust is a separate app. Nexus embeds this into the communication layer itself.

**Lindy AI** — Best onboarding UX. Users create agents by typing plain English: "Create an agent that monitors #support and drafts responses." Has "autopilot mode" where agents use a virtual browser. 4,000+ app integrations. **Steal:** Natural language agent creation. If a Nexus user can type "create an agent that..." in chat, that's the right UX.

**Letta (ex-MemGPT)** — Most important for memory architecture. Key concept: **"think while idle"** — agents process conversations between interactions, consolidate memories, identify gaps, prepare for tomorrow. Agents decide what to remember and forget, not the developer. Agent state is exportable (.af format). **Steal:** Think-while-idle consolidation for the Brain. Overnight, it processes the day's conversations, updates its understanding, generates next-day priorities.

**Artisan AI** — Most aggressive autonomy. AI "employee" Ava sends emails without asking, runs full outbound sequences, self-optimizes based on results. $35M raised, real revenue ($6M+ ARR). **Steal:** Proves market demand for high-autonomy agents. The trust spectrum (not binary) is the right model.

**Glean** — Enterprise Knowledge Graph by ex-Google search engineers. Indexes all company tools and understands relationships between people, projects, and documents. Permission-aware — never leaks info across access boundaries. 100+ actions across Salesforce, Calendar, Asana, etc. **Steal:** The knowledge graph approach. Brain should understand "who owns what" and "which conversations relate to which projects," not just search text.

**CrewAI** — Best observability. Every agent action is traceable with real-time step-by-step tracing. Role-playing metaphor (agents have role, goal, backstory) is intuitive for non-technical users. **Steal:** Audit trail / observability is essential. Users need to see why the Brain did something, especially for autonomous actions.

**OpenClaw** — Self-hosted personal AI assistant with 239k+ GitHub stars. Gateway-as-hub architecture routing to 13+ messaging platforms (Telegram, WhatsApp, Slack, Discord, iMessage, etc.) through one WebSocket control plane. Key innovations: **agent behavior defined in Markdown files** (SOUL.md for persona, AGENTS.md for instructions), **skills as SKILL.md** files (no complex plugin API — just Markdown with YAML frontmatter), **heartbeat scheduler** (cron-like routines for proactive actions), **auto-flush memory** (detects token limit, saves context silently), **hybrid retrieval** (vectors + BM25 + temporal decay). **Steal:** File-as-config pattern, multi-channel gateway, heartbeat system, bounded agent-to-agent communication (max 5 rounds), capability-advertised nodes, layered permission model. **Gap Nexus fills:** OpenClaw is personal (single user). Nexus is team-first (shared Brain, shared memory, shared workspace).

**Mem0 + Zep** — Dedicated memory layers. Mem0: extracts atomic memory statements from interactions (26% accuracy boost, 90% token savings). Zep: temporal knowledge graph where relationships evolve over time. **Steal:** The Brain needs a dedicated memory layer, not ad-hoc context stuffing. Atomic memory extraction + temporal relationships.

### Architecture Patterns to Adopt

| Pattern | Source | Application in Nexus |
|---|---|---|
| Role-based sub-agents | CrewAI | Brain delegates to Scribe, Researcher, Coordinator, TaskMaster |
| Natural language agent creation | Lindy | Users type "create an agent that..." in chat |
| Self-managed persistent memory | Letta | Brain decides what to remember, persists across all sessions |
| Think while idle | Letta | Brain processes conversations overnight, generates morning insights |
| Scheduled/proactive agents | Dust | Daily standups, weekly summaries, deadline nudges without being asked |
| Self-critique before acting | LangGraph | Brain reviews its own output before sending emails or posting |
| Handoffs + guardrails | OpenAI Agents SDK | Brain delegates to sub-agents with safety constraints |
| Enterprise knowledge graph | Glean | Brain maps people → projects → decisions → deadlines |
| Configurable trust spectrum | Artisan | Per-action, per-channel autonomy levels (not binary on/off) |
| Full observability / audit trail | CrewAI, Relevance AI | Every Brain action is traceable and explainable |
| Atomic memory extraction | Mem0 | Extract discrete facts from conversations, not just summaries |
| Temporal knowledge graph | Zep | Relationships evolve over time (who's collaborating now vs 3 months ago) |
| File-as-config agent definition | OpenClaw | SOUL.md, INSTRUCTIONS.md, SKILL.md — plain Markdown, transparent, editable |
| Multi-channel gateway | OpenClaw | One Brain reachable from Telegram, WhatsApp, Slack, email, web |
| Heartbeat scheduler | OpenClaw | Cron-like Brain routines: morning briefs, weekly summaries, idle consolidation |
| Skills as Markdown | OpenClaw | Each skill is a SKILL.md — no plugin API, just instructions the LLM reads |
| Bounded agent communication | OpenClaw | Max rounds, skip tokens, isolated sessions, allowlisted tools |
| Auto-flush memory | OpenClaw | Detect token limit, silently save context, compact session |
| Hybrid retrieval | OpenClaw | Vectors + BM25 keyword + temporal decay for memory search |

### Three Strategic Priorities

1. **Memory is the moat.** Every platform is converging on memory as the differentiator. A Brain that remembers 6 months of team context, preferences, and decisions is exponentially more valuable than one that starts fresh. The longer a team uses Nexus, the harder it is to leave — the Brain knows too much.

2. **Trust is a spectrum, not a switch.** Per-action, per-channel autonomy levels. Teams start with "draft + approve," build confidence, and gradually dial up to full autonomy. The UI must make the current trust level visible and adjustable.

3. **Proactive beats reactive.** The Brain should not wait to be asked. "I noticed the deadline for Project X is in 3 days and no one has updated the status" — that's the magic moment. Scheduled agents (morning briefings, weekly digests) and idle-time processing (overnight consolidation) are essential.

---

## Design Principles

1. **Brain-first** — the AI is the product, chat is the interface to it
2. **Zero friction** — instant workspaces, no signup required, value in seconds
3. **One binary, one directory** — deployment never requires more than one binary and one data directory
4. **Agents are peers** — same identity system, same API, same presence as humans
5. **Privacy by architecture** — classification levels, local LLM option, E2E-ready schema from day one
6. **Local-first option** — always possible to run without internet, on your own hardware
7. **Stands alone without AI** — the platform works as a great team chat + tasks tool even if you never use the Brain. The AI enhances but doesn't gate functionality
8. **Cost-transparent** — users see exactly what the AI costs and can tune the tradeoff
9. **Memory is the moat** — the longer a team uses Nexus, the smarter the Brain gets, the harder it is to leave
10. **Trust is a spectrum** — autonomy is configurable per action, per channel, per agent — never binary
