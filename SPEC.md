# Nexus — Product & Technical Specification

> For one developer + one AI collaborator. Last updated: March 12, 2026.

## What Is Nexus

AI-native team platform: real-time chat, tasks, documents, calendar, files, org chart, and a persistent AI Brain — all in a single self-hosted Go binary with an embedded SvelteKit frontend.

**Stack:** Go 1.25 + SQLite WAL + SvelteKit 2 / Svelte 5 + WebSocket + OpenRouter + xAI Grok + Google Gemini + MCP

**Deploy:** `fly deploy` (production), `make dev` (local :3000), or bare metal with auto-TLS

---

## Product Features

### Chat & Channels
- Public channels, DMs (1:1), group channels with explicit membership
- Real-time send/edit/delete, emoji reactions, threaded replies (`parent_id`)
- Typing indicators, presence (online/offline), unread counts, favorites
- Channel clear (soft-delete), rate limiting (200ms/conn)

### Brain (AI Team Member)
- Persistent AI member in every workspace, responds to @Brain or DM
- System prompt built from definition files: SOUL.md, INSTRUCTIONS.md, TEAM.md, MEMORY.md, HEARTBEAT.md
- 21 built-in tools (tasks, search, docs, calendar, email, telegram, web search, X search, image gen, memory, delegation)
- 2-round tool calling loop: LLM → tool calls → results → final response
- Memory system: fact/decision/commitment/person types, FTS5 search, importance scoring, 30-day recency decay
- Skills: .md files with YAML frontmatter (trigger, channels, roles, autonomy), 15+ templates
- Heartbeat: scheduled actions (daily/weekly/hourly) parsed from HEARTBEAT.md
- Knowledge base: text, file upload, URL import — searchable via tool
- Channel summaries: rolling LLM-generated per-channel context
- WebLLM mode: client-side inference with server-provided context

### AI Agents
- Custom agents with personality (role, goal, backstory, instructions, constraints)
- Per-agent: model, temperature, tools subset, channel access, knowledge/memory access
- Triggers: @mention, DM, schedule, keyword, auto-follow-threads
- 3 builtin agents: Brain (orchestrator), Creative Director (image gen), Caly (EA)
- AI-assisted creation: describe in natural language → full config JSON
- Agent skills: per-agent .md skill files, separate from Brain skills
- Concurrency: semaphore (20 max), conversation tracker prevents loops

### Tasks
- Kanban board: backlog → todo → in_progress → done → cancelled
- Priority: low/medium/high/urgent
- Fields: title, description, expected_output, assignee, due date, tags, position (drag order)
- Brain can create/list/update/delete tasks via tools

### Documents
- Rich text editor (TipTap) + markdown mode toggle
- Sharing: workspace or private, folder assignment
- Full-text indexed in Bleve

### Files
- Content-addressed blob storage (SHA-256 dedup)
- Folder tree with nesting, private flag, drag-to-move
- Upload per channel or to folder, rename, duplicate, delete
- Resizable editor panel for inline note editing

### Calendar
- Events with title, description, location, start/end, all-day, color
- Full RRULE recurrence (daily, weekly, monthly, custom)
- Attendees, reminders (cron-based WebSocket notifications)
- Brain can create/list/update/delete events
- Agent-scheduled events: auto-execute Brain/agents at start_time
- Execution tracking: `executed_at` column, outcome API with response/tools/model
- Outcome viewer: click past agent events → see status (executed/missed), prompt, response, tools used, model
- Bulk clear agent events (past or all)

### Social Pulse
- X/Twitter sentiment analysis via xAI Grok's x_search
- Pipeline: searching → analyzing → ready
- Returns: sentiment score (0-100), summary, themes, key posts, recommendations, citations
- Real-time status via WebSocket, save report as document

### Organization & Teams
- Member profiles: reports_to, title, bio, goals, color
- Custom org roles with filled_by (member or agent)
- 9 RBAC roles, 31 permissions, per-member overrides

### Search
- Bleve full-text: messages, tasks, docs, knowledge, members, channels
- Optional Qdrant vector search (openai/text-embedding-3-small, 1536-dim)

### Activity Stream
- Typed event log with smart dedup (10-min window consolidation)
- Real-time broadcast via WebSocket

### Integrations
- **Email**: inbound SMTP server (:2525), MIME parsing, thread tracking, outbound via SMTP/Resend
- **Telegram**: webhook-based, bi-directional messaging to linked channels
- **WhatsApp**: Twilio + Meta Cloud API, conversation management, 24h window tracking
- **Incoming Webhooks**: token-based ingestion, event log per hook
- **MCP**: stdio + SSE transports, tool discovery, namespaced tools merged with Brain tools

---

## Architecture

### Single Binary
Go backend embeds SvelteKit static output via `//go:embed all:build`. One binary = entire product.

### Database: SQLite
- **Global** (`nexus.db`): accounts, workspaces, JWT secret, platform config
- **Per-workspace** (`workspaces/{slug}/workspace.db`): all workspace data
- WAL mode, lazy connection pool via WorkspaceManager
- Sequential integer migrations tracked in `_migrations` table

### Key Tables (workspace DB, 41 migrations)

| Table | Purpose |
|---|---|
| `members` | Workspace members with roles, profiles, colors |
| `channels` | Public/DM/group channels |
| `messages` | Chat messages with threading (parent_id), soft-delete |
| `reactions` | Emoji reactions on messages |
| `tasks` | Kanban tasks with priority, assignee, position |
| `documents` | Rich text docs with folder/sharing |
| `files` | Content-addressed blobs with metadata |
| `folders` | Nested folder tree |
| `calendar_events` | Events with RRULE recurrence, agent execution tracking |
| `brain_settings` | K/V store for all Brain config |
| `brain_memories` | Typed memories with importance scores |
| `brain_knowledge` | Knowledge base articles |
| `brain_action_log` | Brain action audit trail |
| `agents` | Custom AI agents with full config |
| `mcp_servers` | MCP server connections |
| `social_pulses` | Social sentiment analysis results |
| `activity_stream` | Typed activity events |
| `whatsapp_conversations` | WhatsApp conversation state |
| `webhook_hooks` / `webhook_events` | Incoming webhook config + log |
| `email_threads` | SMTP thread tracking |
| `workspace_models` | Custom model catalog |

### WebSocket Protocol
- Endpoint: `GET /ws?token={jwt}`
- JSON envelope: `{"type": "...", "payload": {...}}`
- One Hub per workspace, auto-subscribe to all non-archived channels
- Two broadcast modes: normal (drop if buffer full) + low-priority (drop if >50% full)
- Keepalive: ping 30s, write timeout 15s, read timeout 90s

### Client → Server Events
`message.send`, `message.edit`, `message.delete`, `reaction.add/remove`, `typing.start`, `channel.join/leave/read/clear`

### Server → Client Events
`message.new/edited/deleted`, `reaction.added/removed`, `typing`, `presence`, `task.created/updated/deleted`, `event.created/updated/deleted/reminder`, `agent.state`, `unread.update`, `activity.new`, `social_pulse.created/updated/deleted`, `whatsapp.*`, `error`

### Auth
- JWT HS256, workspace-scoped tokens
- 9 roles: admin, member, designer, marketing_coordinator, marketing_strategist, researcher, sales, guest, custom
- 31 permissions with per-member overrides
- Superadmin: platform-wide impersonation, workspace management, account bans

### Content-Addressed Storage
Files stored as `{dataDir}/blobs/{sha256}`. Exact dedup. Download by hash is public (no auth) for CDN compatibility.

### AI Concurrency
- Semaphore: max 20 concurrent Brain/agent goroutines
- ConversationTracker prevents re-entrant loops
- Model fallback chain: on 429/503, retries up to 3 alternate models

### Observability
Prometheus metrics: `nexus_llm_calls_total`, `nexus_llm_tokens_total`, `nexus_llm_latency_seconds`, `nexus_tool_calls_total`, `nexus_tool_latency_seconds`, `nexus_agent_executions_total`, `nexus_ws_connections_active`, `nexus_messages_total`

---

## API Routes

### Auth & Workspace
```
POST /api/auth/register
POST /api/auth/login
GET  /api/auth/me                        PUT /api/auth/me
PUT  /api/auth/me/password
POST /api/auth/switch-workspace
GET  /api/auth/workspaces
POST /api/workspaces                     (create)
POST /api/workspaces/{slug}/join
POST /api/join                           (invite code)
GET  /api/workspaces/{slug}
GET  /api/workspaces/{slug}/info
GET  /api/workspaces/{slug}/online
POST /api/workspaces/{slug}/invite
POST /api/workspaces/{slug}/invite/email
```

### Channels & Messages
```
GET    /api/workspaces/{slug}/channels
POST   /api/workspaces/{slug}/channels
DELETE /api/workspaces/{slug}/channels/{id}
DELETE /api/workspaces/{slug}/channels/{id}/members/{mid}
GET    /api/workspaces/{slug}/channels/{id}/messages
GET    /api/workspaces/{slug}/channels/{id}/messages/{mid}/thread
PUT    /api/workspaces/{slug}/channels/{id}/favorite
POST   /api/workspaces/{slug}/channels/{cid}/brain-message
```

### Tasks
```
POST   /api/workspaces/{slug}/tasks
GET    /api/workspaces/{slug}/tasks
GET    /api/workspaces/{slug}/tasks/{id}
PUT    /api/workspaces/{slug}/tasks/{id}
DELETE /api/workspaces/{slug}/tasks/{id}
```

### Documents
```
POST   /api/workspaces/{slug}/documents
GET    /api/workspaces/{slug}/documents
GET    /api/workspaces/{slug}/documents/{id}
PUT    /api/workspaces/{slug}/documents/{id}
DELETE /api/workspaces/{slug}/documents/{id}
```

### Files & Folders
```
POST   /api/workspaces/{slug}/channels/{id}/files
POST   /api/workspaces/{slug}/folders/{id}/files
GET    /api/workspaces/{slug}/files
GET    /api/workspaces/{slug}/files/{hash}           (public download)
PUT    /api/workspaces/{slug}/files/{id}/update
PUT    /api/workspaces/{slug}/files/{id}/move
DELETE /api/workspaces/{slug}/files/{id}/delete
POST   /api/workspaces/{slug}/files/{id}/duplicate
POST   /api/workspaces/{slug}/folders
GET    /api/workspaces/{slug}/folders
PUT    /api/workspaces/{slug}/folders/{id}
DELETE /api/workspaces/{slug}/folders/{id}
```

### Calendar
```
POST   /api/workspaces/{slug}/calendar/events
GET    /api/workspaces/{slug}/calendar/events
GET    /api/workspaces/{slug}/calendar/events/{id}
PUT    /api/workspaces/{slug}/calendar/events/{id}
DELETE /api/workspaces/{slug}/calendar/events/{id}
GET    /api/workspaces/{slug}/calendar/events/{id}/outcome
DELETE /api/workspaces/{slug}/calendar/events/clear-past-agent   ?mode=past|all
```

### Brain & Knowledge
```
GET  /api/workspaces/{slug}/brain/prompt
GET  /api/workspaces/{slug}/brain/settings
PUT  /api/workspaces/{slug}/brain/settings
GET  /api/workspaces/{slug}/brain/definitions/{file}
PUT  /api/workspaces/{slug}/brain/definitions/{file}
GET  /api/workspaces/{slug}/brain/memories
DELETE /api/workspaces/{slug}/brain/memories/{id}
DELETE /api/workspaces/{slug}/brain/memories
POST /api/workspaces/{slug}/brain/memories/pin
GET  /api/workspaces/{slug}/brain/tools
POST /api/workspaces/{slug}/brain/execute-tool
POST /api/workspaces/{slug}/brain/webllm-context
GET  /api/workspaces/{slug}/brain/actions
GET  /api/workspaces/{slug}/brain/skills
GET  /api/workspaces/{slug}/brain/skills/templates
POST /api/workspaces/{slug}/brain/skills/generate
POST /api/workspaces/{slug}/brain/skills
GET  /api/workspaces/{slug}/brain/skills/{file}
PUT  /api/workspaces/{slug}/brain/skills/{file}
PUT  /api/workspaces/{slug}/brain/skills/{file}/toggle
DELETE /api/workspaces/{slug}/brain/skills/{file}
POST /api/workspaces/{slug}/brain/knowledge
POST /api/workspaces/{slug}/brain/knowledge/upload
POST /api/workspaces/{slug}/brain/knowledge/import-url
GET  /api/workspaces/{slug}/brain/knowledge
GET  /api/workspaces/{slug}/brain/knowledge/{id}
PUT  /api/workspaces/{slug}/brain/knowledge/{id}
DELETE /api/workspaces/{slug}/brain/knowledge/{id}
```

### Agents
```
POST   /api/workspaces/{slug}/agents
GET    /api/workspaces/{slug}/agents
GET    /api/workspaces/{slug}/agents/templates
GET    /api/workspaces/{slug}/agents/{id}
PUT    /api/workspaces/{slug}/agents/{id}
DELETE /api/workspaces/{slug}/agents/{id}
POST   /api/workspaces/{slug}/agents/generate
POST   /api/workspaces/{slug}/agents/edit-with-ai
POST   /api/workspaces/{slug}/agents/from-template
GET    /api/workspaces/{slug}/agents/{id}/skills
GET    /api/workspaces/{slug}/agents/{id}/skills/{file}
PUT    /api/workspaces/{slug}/agents/{id}/skills/{file}
DELETE /api/workspaces/{slug}/agents/{id}/skills/{file}
```

### Members & Org Chart
```
GET    /api/workspaces/{slug}/org-chart
PUT    /api/workspaces/{slug}/org-chart/position
PUT    /api/workspaces/{slug}/members/{id}/profile
GET    /api/workspaces/{slug}/members/{id}
PUT    /api/workspaces/{slug}/members/role
PUT    /api/workspaces/{slug}/members/permission
DELETE /api/workspaces/{slug}/members/{id}
GET    /api/workspaces/{slug}/org-chart/roles
POST   /api/workspaces/{slug}/org-chart/roles
PUT    /api/workspaces/{slug}/org-chart/roles/{id}
DELETE /api/workspaces/{slug}/org-chart/roles/{id}
PUT    /api/workspaces/{slug}/org-chart/roles/{id}/fill
```

### Integrations
```
POST   /api/workspaces/{slug}/brain/webhooks
GET    /api/workspaces/{slug}/brain/webhooks
DELETE /api/workspaces/{slug}/brain/webhooks/{id}
GET    /api/workspaces/{slug}/brain/webhooks/{id}/events
GET    /api/workspaces/{slug}/brain/email/threads
DELETE /api/workspaces/{slug}/brain/email/threads/{id}
GET    /api/workspaces/{slug}/brain/telegram/chats
DELETE /api/workspaces/{slug}/brain/telegram/chats/{id}
GET    /api/workspaces/{slug}/whatsapp/conversations
GET    /api/workspaces/{slug}/whatsapp/conversations/{id}
PUT    /api/workspaces/{slug}/whatsapp/conversations/{id}
DELETE /api/workspaces/{slug}/whatsapp/conversations/{id}
```

### MCP, Models, Activity, Social Pulse, Search, Logs
```
GET    /api/mcp-templates
GET    /api/workspaces/{slug}/mcp-servers
POST   /api/workspaces/{slug}/mcp-servers
GET    /api/workspaces/{slug}/mcp-servers/{id}
PUT    /api/workspaces/{slug}/mcp-servers/{id}
DELETE /api/workspaces/{slug}/mcp-servers/{id}
POST   /api/workspaces/{slug}/mcp-servers/{id}/refresh
GET    /api/models/browse
GET    /api/workspaces/{slug}/models
POST   /api/workspaces/{slug}/models
DELETE /api/workspaces/{slug}/models/{id}
GET    /api/workspaces/{slug}/activity
GET    /api/workspaces/{slug}/activity/stats
POST   /api/workspaces/{slug}/social-pulse
GET    /api/workspaces/{slug}/social-pulse
GET    /api/workspaces/{slug}/social-pulse/{id}
DELETE /api/workspaces/{slug}/social-pulse/{id}
GET    /api/workspaces/{slug}/search
GET    /api/workspaces/{slug}/logs
GET    /api/roles
```

### Superadmin
```
GET    /api/admin/stats
GET    /api/admin/workspaces
GET    /api/admin/accounts
PUT    /api/admin/workspaces/{slug}/suspend
DELETE /api/admin/workspaces/{slug}
PUT    /api/admin/accounts/{id}/ban
POST   /api/admin/impersonate
POST   /api/admin/workspaces/{slug}/enter
GET    /api/admin/audit
GET    /api/admin/workspaces/{slug}/detail
GET    /api/admin/workspaces/{slug}/export
PUT    /api/admin/accounts/{id}/password
POST   /api/admin/announcements
DELETE /api/admin/announcements
GET    /api/admin/models
PUT    /api/admin/models
PUT    /api/admin/models/free
```

---

## Frontend Routes

```
/                          → workspace list / redirect
/w/[slug]                  → main workspace (channels + chat + Brain + agents)
/w/[slug]/tasks            → Kanban task board
/w/[slug]/calendar         → Calendar (month/week views)
/w/[slug]/files            → File manager + document editor
/w/[slug]/team             → Team directory + org chart
/w/[slug]/activity         → Activity stream
/w/[slug]/social-pulse     → Social Pulse (X sentiment)
/w/[slug]/logs             → Brain action logs
/admin                     → Platform superadmin
```

---

## Configuration

```toml
# nexus.toml (or env vars: LISTEN, DATA_DIR, DOMAIN, etc.)
listen      = ":8080"
data_dir    = "~/.nexus"
domain      = "nexus.example.com"    # triggers auto-TLS
dev         = false
smtp_listen = ":2525"
redis_url   = "redis://localhost:6379"
qdrant_url  = "localhost:6334"
```

Brain settings (per-workspace, stored in `brain_settings`): OpenRouter API key, model, xAI API key, Telegram bot token, WhatsApp credentials, SMTP config, Brave Search API key.

---

## Build & Deploy

```bash
make dev          # Build web + Go, run on :3000
make build        # Build production binary
cd web && npm run build   # Frontend only
go build ./cmd/nexus/     # Backend only
fly deploy        # Deploy to Fly.io
```

## Ports

| Port | Service |
|---|---|
| 8080 (or 3000 dev) | HTTP + WebSocket |
| 2525 | Inbound SMTP |

## Key Files

| Path | Purpose |
|---|---|
| `cmd/nexus/` | CLI entry point |
| `internal/server/` | All HTTP + WS handlers |
| `internal/brain/` | Brain engine, LLM, memory, skills, tools |
| `internal/db/migrations/` | SQLite migrations |
| `internal/roles/` | RBAC definitions |
| `internal/hub/` | WebSocket hub |
| `internal/mcp/` | MCP client manager |
| `web/src/routes/(app)/` | SvelteKit pages |
| `web/src/lib/` | API client, WS client, stores, editor |
| `web/static/landing.html` | Marketing page |
