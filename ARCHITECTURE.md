# Nexus — Architecture Documentation

## Project Overview

**Nexus** is an AI-native team platform where the AI Brain is the central nervous system of a workspace — not an assistant bolted onto chat, but the connective tissue of the organization. Humans collaborate *through* the Brain. Chat, tasks, notes, knowledge, and decisions all flow through a shared intelligence that remembers everything, connects dots, and delegates to specialized sub-agents.

**The Brain is the product. Chat is just the interface to it.**

**One-liner:** A shared AI brain for your team — instant, private, self-hosted.

---

## Tech Stack

| Layer | Technology | Purpose |
|-------|-----------|---------|
| **Backend** | Go 1.25 | Single-binary server (HTTP + WebSocket + static files) |
| **Frontend** | SvelteKit 2 + Svelte 5 | Reactive SPA with TypeScript |
| **Database** | SQLite 3 | Embedded — global DB + per-workspace DBs |
| **Real-time** | WebSocket (nhooyr.io/websocket) | Pub/sub hub per workspace |
| **Auth** | JWT (HS256, 30-day expiry) | Stateless tokens, role-based permissions |
| **LLM Provider** | OpenRouter API | Cloud LLM access (Claude, GPT, Gemini, etc.) |
| **Image Generation** | Google Gemini API | Direct integration for image-capable models |
| **Rich Text Editor** | Tiptap (ProseMirror) | Markdown editing with slash commands |
| **Visualization** | D3.js + d3-org-chart | Organization chart rendering |
| **Config** | TOML | `~/.nexus/nexus.toml` |
| **Build** | Vite (frontend) + `go build` (backend) | Web assets embedded via `//go:embed` |
| **Metrics** | Prometheus (`/metrics` endpoint) | LLM calls, tool calls, agent executions, WS connections, messages |
| **Task Queue** | asynq (Redis, optional) | Persistent async tasks with retry; goroutine fallback |
| **Vector Search** | Qdrant (gRPC, optional) | Semantic knowledge search with 1536-dim embeddings; SQL LIKE fallback |
| **Embeddings** | OpenRouter (`text-embedding-3-small`) | Reuses existing API key for knowledge vectorization |

### Key Dependencies (Go)
- `github.com/mattn/go-sqlite3` — SQLite driver (CGO)
- `github.com/golang-jwt/jwt/v5` — JWT signing/validation
- `nhooyr.io/websocket` — WebSocket server
- `github.com/BurntSushi/toml` — Config parsing
- `golang.org/x/crypto` — bcrypt, TLS/ACME
- `github.com/prometheus/client_golang` — Prometheus metrics
- `github.com/hibiken/asynq` — Redis-backed task queue
- `github.com/qdrant/go-client` — Qdrant vector DB (gRPC)

### Key Dependencies (Web)
- `@tiptap/*` — Rich text editor with code blocks, images, tasks, links
- `d3` + `d3-org-chart` — Organization visualization
- `lowlight` — Code syntax highlighting
- `tippy.js` — Tooltip popovers

---

## Vision & Market Position

Nexus sits at the intersection of AI and privacy — an unsolved combination. Slack has AI but zero privacy. Signal has privacy but zero team productivity features.

| | Slack | Teams | Mattermost | **Nexus** |
|---|---|---|---|---|
| Self-hosted | No | No | Complex | **One binary** |
| AI Agents | Bolt-on | Copilot (cloud) | Plugins | **Core architecture** |
| Local LLM | No | No | No | **Planned (llama.cpp)** |
| Data jurisdiction | US | US | Your choice | **Your choice** |
| Zero-friction start | No | No | No | **Instant workspaces** |

Target: law firms (privilege), healthcare (HIPAA), finance (SOX), government (ITAR), EU companies (GDPR), and any privacy-conscious organization or AI-native startup.

### Deployment Model
- **Cloud mode:** Multi-tenant, workspaces as isolated SQLite files
- **Self-hosted:** Same binary, same features, company controls everything
- **Single command:** `./nexus serve --data-dir /var/nexus --port 443`

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           CLIENTS                                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │ Web App  │  │ Telegram │  │ WhatsApp │  │  Slack   │  │  Email   │ │
│  │(SvelteKit│  │   Bot    │  │  Bridge  │  │  Bridge  │  │ Clients  │ │
│  │  SPA)    │  │ (future) │  │ (future) │  │ (future) │  │(CC Brain)│ │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘ │
│       └──────┬───────┴──────────────┴──────────────┴──────────────┘     │
│         WebSocket / REST API                                            │
└──────────────┬──────────────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    NEXUS SERVER (single Go binary)                      │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                   HTTP / WebSocket Layer                           │  │
│  │  ┌──────────┐  ┌──────────────┐  ┌──────────┐  ┌─────────────┐  │  │
│  │  │  Static  │  │  WebSocket   │  │ REST API │  │  Auth       │  │  │
│  │  │  Files   │  │   Hub        │  │  50+     │  │ Middleware  │  │  │
│  │  │(embedded)│  │(per-workspace│  │ endpoints│  │ (JWT+RBAC)  │  │  │
│  │  └──────────┘  │  pub/sub)    │  └──────────┘  └─────────────┘  │  │
│  │                └──────────────┘                                   │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                    BRAIN ENGINE (per workspace)                    │  │
│  │                                                                   │  │
│  │  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────────┐  │  │
│  │  │ The Brain   │  │ Sub-Agents   │  │ Memory System           │  │  │
│  │  │             │  │              │  │                         │  │  │
│  │  │ @mention →  │  │ Creative Dir │  │ L1: Raw Messages (SQL) │  │  │
│  │  │ LLM call →  │  │ Sales Asst   │  │ L2: Channel Summaries  │  │  │
│  │  │ tool exec → │  │ Support      │  │ L3: Extracted Facts    │  │  │
│  │  │ response    │  │ Custom...    │  │    (decisions, people) │  │  │
│  │  └─────────────┘  └──────────────┘  └─────────────────────────┘  │  │
│  │                                                                   │  │
│  │  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────────┐  │  │
│  │  │ Skills      │  │ Knowledge    │  │ Heartbeat Scheduler     │  │  │
│  │  │ .md files   │  │ Base         │  │ Cron-like routines      │  │  │
│  │  │ per agent   │  │ (docs, URLs, │  │ driven by HEARTBEAT.md  │  │  │
│  │  │             │  │  vectors)    │  │                         │  │  │
│  │  └─────────────┘  └──────────────┘  └─────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                    TOOL ACCESS                                     │  │
│  │  ┌───────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐            │  │
│  │  │OpenRouter │ │ Gemini   │ │ Internal │ │ MCP      │            │  │
│  │  │(cloud LLM)│ │(image gen│ │ (tasks,  │ │(stdio/SSE│            │  │
│  │  │           │ │ API)     │ │ docs,    │ │ tools)   │            │  │
│  │  │           │ │          │ │ search)  │ │          │            │  │
│  │  └───────────┘ └──────────┘ └──────────┘ └──────────┘            │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                    OBSERVABILITY & ASYNC                           │  │
│  │  ┌────────────────┐  ┌──────────────┐  ┌──────────────────────┐  │  │
│  │  │ Prometheus     │  │ asynq        │  │ Qdrant               │  │  │
│  │  │ GET /metrics   │  │ (Redis queue)│  │ (vector search)      │  │  │
│  │  │ LLM, tools, WS │  │ memory ext.  │  │ knowledge embeddings │  │  │
│  │  │ latency, counts│  │ summarization│  │ 1536-dim cosine      │  │  │
│  │  └────────────────┘  └──────────────┘  └──────────────────────┘  │  │
│  │  (all optional — graceful fallback when services unavailable)     │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                    DATA LAYER                                      │  │
│  │  ┌────────────────────┐  ┌──────────────────────────────────────┐ │  │
│  │  │  nexus.db (global) │  │  workspaces/{slug}/                  │ │  │
│  │  │  - accounts        │  │  ├── workspace.db (messages, tasks,  │ │  │
│  │  │  - sessions        │  │  │   members, agents, memories...)   │ │  │
│  │  │  - workspace index │  │  ├── brain/ (SOUL.md, INSTRUCTIONS,  │ │  │
│  │  │  - platform models │  │  │   TEAM.md, HEARTBEAT.md, skills/) │ │  │
│  │  │  - admin audit log │  │  └── blobs/ (files, images by hash)  │ │  │
│  │  └────────────────────┘  └──────────────────────────────────────┘ │  │
│  └───────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Directory Structure

```
nexus/
├── cmd/nexus/
│   └── main.go                    # Entry point, CLI (serve/version/help)
├── internal/
│   ├── auth/
│   │   ├── jwt.go                 # JWT generation, validation, claims
│   │   └── middleware.go          # HTTP auth middleware
│   ├── brain/
│   │   ├── brain.go               # Brain init, default definition files
│   │   ├── memory.go              # Memory types, summaries, extraction prompts
│   │   ├── heartbeat.go           # Cron-like scheduled actions
│   │   ├── knowledge.go           # Knowledge base management + semantic search
│   │   ├── skills.go              # Skill loading, parsing, context building
│   │   ├── tools.go               # Tool definitions (OpenAI function calling format)
│   │   ├── actionlog.go           # Brain action audit trail
│   │   ├── templates.go           # Agent templates (Sales, Support, PM, etc.)
│   │   ├── openrouter.go          # OpenRouter API client + embeddings
│   │   ├── gemini_image.go        # Gemini image generation API
│   │   └── builtin_agents.go      # Built-in agent definitions
│   ├── config/
│   │   └── config.go              # TOML config + CLI flags
│   ├── db/
│   │   ├── db.go                  # SQLite init, workspace DB manager
│   │   └── migrations/
│   │       └── migrations.go      # Schema migrations (global + workspace)
│   ├── hub/
│   │   ├── hub.go                 # WebSocket connection manager, pub/sub
│   │   └── protocol.go            # Message types, envelope, payloads
│   ├── id/
│   │   └── id.go                  # Unique ID generation
│   ├── metrics/
│   │   └── metrics.go             # Prometheus metric definitions (promauto)
│   ├── roles/
│   │   ├── roles.go               # Role definitions, permission maps
│   │   └── checker.go             # Permission checking logic
│   ├── vectorstore/
│   │   └── vectorstore.go         # Qdrant gRPC wrapper (upsert, search, delete)
│   └── server/
│       ├── server.go              # Server init, route registration
│       ├── api.go                 # REST API handlers
│       ├── auth.go                # Register, login, account endpoints
│       ├── ws.go                  # WebSocket handler, message routing
│       ├── workspace.go           # Workspace CRUD
│       ├── channels.go            # Channel management
│       ├── members.go             # Member management
│       ├── tasks.go               # Task CRUD
│       ├── documents.go           # Document/notes CRUD
│       ├── files.go               # File upload/download (blob store)
│       ├── contacts.go            # CRM contacts
│       ├── permissions.go         # Permission middleware helpers
│       ├── admin.go               # Superadmin endpoints
│       ├── org_chart.go           # Org structure visualization
│       ├── agents.go              # Agent CRUD, templates
│       ├── brain.go               # Brain settings, definitions API
│       ├── brain_heartbeat.go     # Heartbeat scheduling
│       ├── brain_knowledge.go     # Knowledge base API + vector embedding
│       ├── taskqueue.go           # asynq task queue with goroutine fallback
│       ├── brain_memory.go        # Memory extraction, channel summaries
│       ├── brain_skills.go        # Skill management API
│       ├── brain_tools.go         # Tool execution, Brain mention handler
│       ├── agent_runtime.go       # Agent execution, tool routing
│       └── models.go              # LLM model management
├── web/
│   ├── embed.go                   # //go:embed build/* (assets into binary)
│   ├── src/
│   │   ├── lib/
│   │   │   ├── api.ts             # Fetch wrapper, token management
│   │   │   ├── ws.ts              # WebSocket client, event subscriptions
│   │   │   ├── stores/
│   │   │   │   └── workspace.ts   # Svelte reactive state store
│   │   │   ├── components/
│   │   │   │   └── OrgChart.svelte
│   │   │   └── editor/
│   │   │       ├── TiptapEditor.svelte
│   │   │       └── extensions/
│   │   │           └── slash-commands.ts
│   │   ├── routes/
│   │   │   ├── +page.svelte       # Home (workspace list/creation)
│   │   │   ├── admin/
│   │   │   │   └── +page.svelte   # Superadmin dashboard
│   │   │   └── w/[slug]/
│   │   │       └── +page.svelte   # Main workspace UI (~5600 lines)
│   │   ├── app.css                # Global styles, CSS variables
│   │   └── app.html               # HTML shell
│   ├── svelte.config.js
│   ├── vite.config.ts
│   └── package.json
├── Makefile                       # build, dev, web, clean targets
├── ARCHITECTURE.md                # This file
├── SPEC.md                        # Full product specification
└── PLAN.md                        # Implementation roadmap
```

**Codebase size:** ~11K lines Go, ~8.5K lines Svelte/TypeScript, 45 Go files, 14 web source files.

---

## Data Flow

### 1. User Sends a Message
```
Browser → WebSocket → Hub → broadcast to channel
                         └→ check: is Brain/Agent mentioned?
                              ├── Yes → LLM call → tool execution loop → response in channel
                              └── No  → track message count → maybe extract memories
```

### 2. Brain Mention (`@Brain`)
```
Message with @Brain → handleBrainMentionWithTools()
  1. Load brain definition files (SOUL.md, INSTRUCTIONS.md, TEAM.md, MEMORY.md)
  2. Load heartbeat context
  3. Load relevant memories (facts, decisions, commitments)
  4. Load knowledge base entries
  5. Load channel summary (rolling history)
  6. Load cross-channel summaries (other channels)
  7. Fetch last 40 messages
  8. Build system prompt + user messages
  9. Call OpenRouter API with tool definitions
  10. Execute tool calls (create_task, search, generate_image, etc.)
  11. Loop until no more tool calls or max iterations
  12. Post response as Brain message
```

### 3. Agent Mention (`@Creative Director`)
```
Message with @Agent → handleAgentMention()
  1. Load agent config (role, goal, backstory, instructions)
  2. Load agent-specific skills (full body, not just names)
  3. Load workspace memories
  4. Load channel summary
  5. Fetch last 40 messages
  6. Build system prompt with agent personality
  7. Call OpenRouter API with agent's permitted tools
  8. Route tool calls through executeAgentTool()
     └→ generate_image → two-stage enrichment pipeline
  9. Post response as agent message
```

### 4. Image Generation (Agent Pipeline)
```
Agent requests generate_image → toolGenerateImageForAgent()
  1. Build agent context (name, role, goal, backstory)
  2. Load relevant skill body (e.g., Ad Creative playbook)
  3. Call enrichment LLM (OpenRouter) with PromptEnrichmentSystemPrompt
     → transforms brief into structured prompt (subject, headline, CTA, layout, etc.)
  4. Call Gemini API with enriched prompt
  5. Decode base64 image → save as blob (SHA-256 hash)
  6. Return markdown image ref + <image-prompt> tag with enriched prompt
  7. UI renders collapsible prompt display below image
```

### 5. Memory Extraction
```
Every N messages (default 15) → trackMessageAndMaybeExtract()
  1. Increment per-channel counter (initialized from DB on restart)
  2. When threshold hit:
     a. extractMemories(): LLM extracts facts, decisions, commitments, people
     b. updateChannelSummary(): LLM generates/merges rolling channel summary
  3. Memories saved to brain_memories table
  4. Summary saved to brain_channel_summaries table
```

---

## Database Schema

### Global Database (`~/.nexus/nexus.db`)

| Table | Purpose |
|-------|---------|
| `accounts` | User accounts (id, email, password_hash, display_name, is_superadmin, banned) |
| `workspaces` | Workspace index (slug, name, created_by, suspended) |
| `sessions` | JWT sessions (token, account_id, workspace_slug, expires_at) |
| `invite_tokens` | Workspace invite links (token, workspace_slug, created_by) |
| `jwt_secrets` | Single JWT signing key (32-byte random) |
| `admin_audit_log` | Superadmin actions (actor, action, target, detail) |
| `platform_announcements` | Platform-wide messages (message, type, active) |
| `platform_models` | Pinned LLM models (id, display_name, provider, context_length) |

### Workspace Database (`~/.nexus/workspaces/{slug}/workspace.db`)

| Table | Purpose |
|-------|---------|
| `members` | Team members (account_id, display_name, role, title, bio, reports_to) |
| `channels` | Chat rooms (name, type: public/private/dm, classification, archived) |
| `messages` | Messages (channel_id, sender_id, content, deleted, created_at) |
| `reactions` | Emoji reactions (message_id, user_id, emoji) |
| `channel_reads` | Read receipts (channel_id, user_id, last_read_at) |
| `tasks` | Task items (title, status, priority, assignee_id, due_date, tags) |
| `documents` | Notes/pages (title, content, sharing, channel_id) |
| `files` | File metadata (name, mime, size, hash, channel_id) |
| `contacts` | CRM records (name, email, phone, company) |
| `agents` | AI agent configs (name, role, goal, backstory, model, tools, triggers) |
| `org_roles` | Organization structure (title, description, reports_to, filled_by) |
| `brain_settings` | Key-value config (api_key, model, gemini_api_key, memory_enabled, etc.) |
| `brain_memories` | Extracted facts (type: fact/decision/commitment/person, content) |
| `brain_channel_summaries` | Rolling summaries (channel_id, summary, message_count, last_message_id) |
| `brain_knowledge` | Knowledge base entries (title, content, source_type, tokens) |
| `brain_action_log` | Brain action audit (action_type, channel_id, trigger, response, model) |
| `permission_overrides` | Per-member permission exceptions |
| `guest_channels` | Channel access for guest role |

---

## WebSocket Protocol

Messages use a JSON envelope format:

```json
{
  "type": "message.send",
  "payload": { "channel_id": "...", "content": "..." }
}
```

### Message Types

| Type | Direction | Purpose |
|------|-----------|---------|
| `message.send` | client → server | Send a message |
| `message.new` | server → client | New message broadcast |
| `message.edit` | client → server | Edit a message |
| `message.edited` | server → client | Edit broadcast |
| `message.delete` | client → server | Delete a message |
| `message.deleted` | server → client | Deletion broadcast |
| `reaction.add/remove` | client → server | Add/remove emoji reaction |
| `reaction.added/removed` | server → client | Reaction broadcast |
| `typing.start/stop` | client → server | Typing indicators |
| `typing` | server → client | Typing broadcast |
| `presence.update` | client → server | Update presence status |
| `presence` | server → client | Presence broadcast |
| `channel.join` | client → server | Join a channel |
| `channel.joined` | server → client | Join broadcast |
| `channel.clear` | client → server | Clear DM messages (admin) |
| `channel.cleared` | server → client | Clear broadcast |
| `task.created/updated/deleted` | server → client | Task change broadcasts |
| `agent.state` | server → client | Agent state (thinking, tool_executing, idle) |
| `error` | server → client | Error message |

---

## Brain System

### Definition Files (per workspace)

Each workspace has a `brain/` directory with markdown files that define the Brain's personality and behavior:

| File | Purpose | Updated by |
|------|---------|-----------|
| `SOUL.md` | Personality, tone, boundaries | Admin (via UI) |
| `INSTRUCTIONS.md` | Workspace-specific operating rules | Admin (via UI) |
| `TEAM.md` | Team member context (who, roles, relationships) | Auto-updated |
| `MEMORY.md` | Long-term curated knowledge | Auto-extracted |
| `HEARTBEAT.md` | Scheduled routines (cron-like definitions) | Admin (via UI) |
| `skills/*.md` | Modular capability definitions | Admin (via UI) |

### System Prompt Assembly

When Brain or an agent responds, the system prompt is assembled in layers:

```
┌────────────────────────────────────────────┐
│ 1. Brain Definition Files (~2K tokens)     │
│    SOUL.md + INSTRUCTIONS.md + TEAM.md     │
│    + MEMORY.md + HEARTBEAT.md              │
├────────────────────────────────────────────┤
│ 2. Extracted Memories (~1K tokens)         │
│    Facts, decisions, commitments, people   │
├────────────────────────────────────────────┤
│ 3. Knowledge Base (~1K tokens)             │
│    Uploaded docs, imported URLs            │
├────────────────────────────────────────────┤
│ 4. Skills (~1K tokens)                     │
│    Skill definitions relevant to context   │
├────────────────────────────────────────────┤
│ 5. Channel Summary (~500 tokens)           │
│    Rolling summary of older messages       │
├────────────────────────────────────────────┤
│ 6. Cross-Channel Awareness (~500 tokens)   │
│    Summaries from other channels           │
│    (Brain only — agents are channel-scoped)│
├────────────────────────────────────────────┤
│ 7. Recent Messages (last 40) (~6K tokens)  │
│    Full message content                    │
└────────────────────────────────────────────┘
Total: ~12K tokens — within 128K context windows
```

### Brain Tools

Seven tools available via OpenAI-compatible function calling:

| Tool | Purpose | Parameters |
|------|---------|-----------|
| `create_task` | Create workspace tasks | title, description, status, priority, assignee_name |
| `list_tasks` | Query tasks with filters | status, assignee_name |
| `search_messages` | Search message history | query, channel_id, limit |
| `create_document` | Create markdown documents | title, content |
| `search_knowledge` | Query knowledge base | query |
| `delegate_to_agent` | Route work to sub-agents | agent_name, task |
| `generate_image` | Generate images via Gemini | prompt |

### Agents

Agents are specialized AI personalities with scoped tools and triggers:

**Built-in Agent:**
- **Creative Director** — Campaign concepts, ad visuals, brand consistency. Uses `google/gemini-2.5-flash` for image-capable responses. Has a two-stage image generation pipeline with prompt enrichment.

**Agent Templates (create from UI):**
- Sales Assistant, Support Triage, Meeting Scribe, Content Writer, Research Analyst, Onboarding Buddy, Legal Reviewer, Project Manager

**Agent Config:**
- Personality: role, goal, backstory, instructions
- LLM: model, temperature, max_tokens
- Capabilities: tools list, knowledge_access, memory_access, can_delegate
- Guardrails: max_iterations, requires_approval, constraints, escalation_prompt
- Triggers: mention, all, always
- Skills: per-agent `.md` files with playbooks and instructions

---

## Auth & Permissions

### Authentication
- **JWT tokens** — HS256, 30-day expiry, extracted from `Authorization: Bearer` header or `nexus_token` cookie
- **Registration** — email + password + display name → bcrypt hash → account created
- **Login** — email + password → JWT issued with claims (userID, role, workspace, superadmin flag)

### Role-Based Access Control

**9 roles** with hierarchical permissions:

| Role | Access Level |
|------|-------------|
| `admin` | Full workspace control |
| `member` | Standard chat, tasks, contacts, Brain access |
| `designer` | Member + contact management |
| `marketing_coordinator` | Member + contact management |
| `marketing_strategist` | Member + contact management |
| `researcher` | Member minus task assign/delete |
| `sales` | Member + full CRM access |
| `guest` | Read + send messages, react, view contacts only |
| `custom` | Per-member permission overrides |

**22 permissions** across chat, channels, tasks, contacts, brain, and workspace operations. Per-member overrides stored in `permission_overrides` table.

---

## API Routes (50+ endpoints)

### Auth
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/auth/register` | Create account |
| POST | `/api/auth/login` | Login |
| GET | `/api/auth/me` | Current user |
| PUT | `/api/auth/me` | Update profile |
| GET | `/api/auth/workspaces` | User's workspaces |

### Workspaces & Channels
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/workspaces` | Create workspace |
| GET | `/api/workspaces/{slug}` | Get workspace |
| POST | `/api/workspaces/{slug}/join` | Join workspace |
| POST | `/api/workspaces/{slug}/invite` | Create invite |
| GET | `/api/workspaces/{slug}/channels` | List channels |
| POST | `/api/workspaces/{slug}/channels` | Create channel |
| GET | `/api/workspaces/{slug}/channels/{id}/messages` | Message history |
| GET | `/api/workspaces/{slug}/online` | Online members |

### Tasks, Documents, Files, Contacts
| Method | Path | Purpose |
|--------|------|---------|
| CRUD | `/api/workspaces/{slug}/tasks` | Task management |
| CRUD | `/api/workspaces/{slug}/documents` | Document management |
| POST | `/api/workspaces/{slug}/channels/{id}/files` | File upload |
| GET | `/api/workspaces/{slug}/files/{hash}` | File download |
| CRUD | `/api/workspaces/{slug}/contacts` | CRM contacts |

### Brain
| Method | Path | Purpose |
|--------|------|---------|
| GET/PUT | `/api/workspaces/{slug}/brain/settings` | Brain configuration |
| GET/PUT | `/api/workspaces/{slug}/brain/definitions/{file}` | Definition files |
| GET | `/api/workspaces/{slug}/brain/memories` | List memories |
| DELETE | `/api/workspaces/{slug}/brain/memories` | Clear all memories |
| GET | `/api/workspaces/{slug}/brain/actions` | Action log |
| CRUD | `/api/workspaces/{slug}/brain/skills/{file}` | Skill management |
| CRUD | `/api/workspaces/{slug}/brain/knowledge` | Knowledge base |

### Agents
| Method | Path | Purpose |
|--------|------|---------|
| CRUD | `/api/workspaces/{slug}/agents` | Agent management |
| GET | `/api/workspaces/{slug}/agents/templates` | Agent templates |
| POST | `/api/workspaces/{slug}/agents/generate` | AI-generate agent config |
| POST | `/api/workspaces/{slug}/agents/from-template` | Create from template |
| CRUD | `/api/workspaces/{slug}/agents/{id}/skills/{file}` | Agent skills |

### Admin (superadmin)
| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/admin/stats` | Platform statistics |
| GET | `/api/admin/workspaces` | All workspaces |
| GET | `/api/admin/accounts` | All accounts |
| PUT | `/api/admin/workspaces/{slug}/suspend` | Suspend workspace |
| PUT | `/api/admin/accounts/{id}/ban` | Ban account |
| POST | `/api/admin/impersonate` | Impersonate user |

---

## Data Directory Layout

```
~/.nexus/
├── nexus.toml                         # Server configuration
├── nexus.db                           # Global database
├── nexus.db-wal                       # SQLite WAL
└── workspaces/
    └── {slug}/
        ├── workspace.db               # Workspace database
        ├── blobs/                     # Content-addressed file storage
        │   └── {2-char-prefix}/
        │       └── {sha256-hash}      # Binary file data
        └── brain/
            ├── SOUL.md                # Brain personality
            ├── INSTRUCTIONS.md        # Operating rules
            ├── TEAM.md                # Team context
            ├── MEMORY.md              # Long-term knowledge
            ├── HEARTBEAT.md           # Scheduled routines
            ├── skills/                # Brain skills
            │   ├── daily-standup.md
            │   ├── meeting-notes.md
            │   ├── decision-logger.md
            │   └── ...
            └── agents/
                └── {agent_id}/
                    └── skills/        # Agent-specific skills
                        ├── ad-creative.md
                        └── ...
```

---

## Build & Development

```bash
# Development (builds web + Go, runs with --dev flag)
make dev

# Production build
make web          # npm run build → web/build/
make build        # go build -o nexus ./cmd/nexus/ (embeds web/build/)

# Run
./nexus serve --listen :8080 --dev
./nexus serve --data-dir /var/nexus --domain nexus.example.com  # auto-TLS

# Clean
make clean
```

**Important:** Web assets are embedded at Go compile time via `//go:embed`. After `npm run build`, you must restart the Go server to pick up frontend changes.

---

## Key Architectural Patterns

1. **Hub-and-spoke messaging** — One goroutine-based hub per workspace, connections fan out to all channel members
2. **Multi-tenant SQLite** — Global DB for accounts/auth, isolated workspace DBs for complete data separation
3. **Brain as first-class citizen** — Brain is a member of every workspace, participates in channels like a human
4. **Tool execution loop** — LLM calls return tool_calls → server executes → feeds results back → loops until done or max iterations
5. **Skill-driven behavior** — Markdown files define agent capabilities, loaded into system prompt at runtime
6. **Content-addressed storage** — Files stored by SHA-256 hash, deduplication built in
7. **Single binary deployment** — All assets embedded, no external dependencies except SQLite (via CGO)
8. **Middleware chain** — Auth → permission check → handler, composable with `requirePerm()`
