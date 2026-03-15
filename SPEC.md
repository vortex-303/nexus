# Nexus — Product & Engineering Specification

**Version:** March 2026
**Production:** https://nexusteams.dev/

---

## 1. Product Overview

Nexus is an AI-native team platform where a persistent AI Brain is the central nervous system of every workspace. The Brain remembers everything, extracts decisions, connects dots across channels, delegates to specialized sub-agents, and executes real actions (tasks, calendar events, emails, documents) through natural language.

**One-liner:** A shared AI brain for your team — instant, private, self-hosted.

**Target markets:** Law firms (privilege), healthcare (HIPAA), finance (SOX), EU companies (GDPR), government (ITAR), and any privacy-conscious team that wants AI without sending data to a third party.

**Positioning:**

| | Slack | Teams | Mattermost | Nexus |
|---|---|---|---|---|
| Self-hosted | No | No | Complex | One binary |
| AI Agents | Bolt-on | Copilot (cloud) | Plugins | Core architecture |
| Local LLM | No | No | No | Planned |
| Data jurisdiction | US | US | Your choice | Your choice |

---

## 2. Architecture

### 2.1 Single Binary

Nexus compiles to a single Go binary embedding everything: HTTP + WebSocket server, REST API, SvelteKit SPA (`//go:embed all:build`), SQLite migrations, Brain engine, tool executor, and memory extractor. No runtime dependencies beyond the binary itself.

```bash
./nexus serve --data-dir /var/nexus --domain nexus.example.com
```

### 2.2 Database Model

SQLite with WAL mode, dual-database design:

**Global database** (`nexus.db`):
- `accounts` — email, bcrypt password, display name, superadmin flag
- `workspaces` — slug, name, created_by, suspended, max_members
- `sessions` — JWT sessions
- `invite_tokens` — invite links with optional expiry
- `jwt_secrets` — HS256 signing key
- `admin_audit_log` — superadmin action trail
- `platform_announcements` — platform-wide banners
- `platform_models` — pinned LLM model catalog
- `email_verifications` — verification codes
- `password_resets` — reset tokens
- `waitlist` — waitlist signups (email, plan)

**Per-workspace database** (`workspaces/{slug}/workspace.db`) — completely isolated per tenant:
- `members` — account_id, display_name, role, title, bio, goals, reports_to, color
- `channels` — name, type (public/private/dm), classification, archived
- `messages` — channel_id, sender_id, content, parent_id (threads), deleted, metadata
- `reactions` — emoji reactions
- `channel_reads` — read receipts + favorites
- `channel_members` — private channel membership
- `permission_overrides` — per-member permission exceptions
- `guest_channels` — allowed channels for guest role
- `tasks` — title, description, expected_output, status, priority, assignee_id, due_date, tags, position, scheduled_at, recurrence_rule, agent_id
- `documents` — title, content, sharing, channel_id, folder_id
- `files` — name, mime, size, hash, channel_id, folder_id, is_private
- `folders` — hierarchical folder organization
- `agents` — AI agent configs (personality, LLM config, tools, triggers, behavior)
- `org_roles` — organizational roles (title, reports_to, filled_by)
- `brain_settings` — key-value config (api_key, model, etc.)
- `brain_settings_log` — audit log of settings changes
- `brain_memories` — extracted memories (type, content, source, importance, confidence, participants, valid_until, superseded_by)
- `brain_memories_fts` — FTS5 virtual table for full-text memory search
- `brain_channel_summaries` — rolling channel summaries
- `brain_knowledge` — knowledge base articles (title, content, source_type, source_name, source_url, tokens)
- `brain_action_log` — Brain action audit trail
- `mcp_servers` — MCP server configurations
- `webhook_hooks` — incoming webhook definitions
- `webhook_events` — webhook delivery log
- `channel_integrations` — Telegram, email mappings
- `email_threads` — inbound email tracking
- `calendar_events` — events with recurrence, attendees, reminders
- `calendar_reminders_sent` — dedup log
- `workspace_models` — workspace-pinned LLM models
- `free_models` — free-tier accessible models
- `activity_stream` — activity feed
- `social_pulses` — social media intelligence scans
- `living_briefs` — auto-generated intelligence briefs
- `llm_usage` — token and cost tracking
- `reflection_history` — periodic Brain reflection records

### 2.3 Frontend

- SvelteKit 2 + Svelte 5 runes (`$state`, `$derived`, `$effect`)
- Static adapter — pure SPA embedded in binary
- TypeScript throughout
- Tiptap (ProseMirror) rich text editor with slash commands, code blocks, images, task lists
- Dark theme, orange accent (`#f97316`)
- D3.js + d3-org-chart for org visualization
- WebSocket client with typed event subscriptions

### 2.4 Real-Time Layer

WebSocket hub per workspace using JSON envelopes:

```json
{ "type": "message.send", "payload": { "channel_id": "...", "content": "..." } }
```

Key event types: `message.*`, `reaction.*`, `typing.*`, `presence.*`, `channel.*`, `task.*`, `agent.state`

Auth via `?token=` query parameter (JWT). 256-message buffer per connection; low-priority messages dropped when buffer > 50%.

### 2.5 Deployment

**Fly.io (hosted):** `shared-cpu-1x`, 1GB RAM, persistent volume at `/data`, auto-suspend/resume, health check at `GET /health`.

**Self-hosted:** Same binary. Pass `--domain` for auto-TLS via Let's Encrypt (`autocert`).

---

## 3. Core Features

### 3.1 Workspace Management
- Create workspace (email+password in cloud mode); creator becomes admin; `#general` + Brain auto-created
- Join by invite link, email invite, or code
- Export/import full workspace JSON
- Destroy workspace (admin kill switch)

### 3.2 Real-Time Messaging
- Public/private channels and DMs
- Threaded replies, emoji reactions, typing indicators
- Read receipts and unread counts
- Message edit/delete, channel favorites
- Rich metadata (attachments, images, Brain response data)

### 3.3 File Uploads
- Content-addressed blobs (SHA-256 dedup)
- Stored at `workspaces/{slug}/blobs/{2-char-prefix}/{hash}`
- Hierarchical folder organization with privacy flags
- Any MIME type; images rendered inline

### 3.4 Tasks
- Statuses: `backlog`, `todo`, `in_progress`, `done`, `cancelled`
- Priorities: `low`, `medium`, `high`, `urgent`
- Assignee, due date, tags, position (drag-to-reorder)
- Scheduled tasks with RRULE recurrence
- Agent-linked tasks
- Brain can create/list/update/delete via natural language

### 3.5 Documents / Wiki
- Markdown with Tiptap rich-text editing
- Folder organization, sharing modes (workspace or channel-scoped)

### 3.6 Calendar
- Events with start/end, all-day, RRULE recurrence, attendees, reminders
- iCal export
- Reminder cron fires every minute
- Agent-driven autonomous calendar events

### 3.7 Member Roles & Permissions

Three base roles: `admin` (all), `member` (standard), `guest` (restricted).

31 permissions across: chat, channels, tasks, brain, agents, documents, files, knowledge, calendar, skills, workspace, contacts. Per-member overrides supported. Agents/Brain are special roles excluded from member limits.

### 3.8 Search
Bleve full-text search with auto-reindex on startup. Brain uses `search_workspace` tool (SQL LIKE fallback) and optional Qdrant semantic vector search for knowledge.

### 3.9 Authentication
- Email + password with bcrypt
- Email verification (6-digit code, 24h expiry) via Resend
- Password reset (1h token, single use)
- JWT HS256, 30-day expiry, workspace-scoped
- Superadmin impersonation

### 3.10 Activity Feed
- All significant events tracked in `activity_stream`
- Message consolidation: one entry per actor+channel per 10-minute window
- Aggregate stats endpoint

---

## 4. Brain AI System

### 4.1 Overview

Brain is a first-class workspace member (fixed ID `"brain"`, role `"brain"`). Triggered by `@Brain` in any channel or via DM. Uses OpenRouter API (any model). Tool-calling loop: LLM -> tool calls -> execute -> results -> loop until done.

### 4.2 System Prompt Assembly (~12K tokens)

1. **Definition files** (~2K): `SOUL.md`, `INSTRUCTIONS.md`, `TEAM.md`, `MEMORY.md`, `REFLECTIONS.md`, `HEARTBEAT.md`
2. **Current time** — UTC timestamp + day of week
3. **Extracted memories** (~1K): facts, decisions, commitments, people — ranked by BM25 + recency + importance
4. **Knowledge base** (~1K): all if <=5000 tokens, else keyword/semantic search
5. **Skills** (~1K): active skill definitions
6. **Channel summary** (~500): rolling LLM summary of older messages
7. **Cross-channel awareness** (~500): summaries from up to 5 other channels
8. **Recent messages** (~6K): last 40 messages with sender names
9. **Workspace snapshot** (`BuildWorkspaceContext`): channels, members, task counts + 5 active, 5 recent docs, 5 upcoming events

### 4.3 Tools

| Tool | Purpose |
|------|---------|
| `create_task` | Create a task |
| `list_tasks` | List/filter tasks |
| `update_task` | Update task fields |
| `delete_task` | Delete a task |
| `search_workspace` | Full-text search messages, tasks, documents |
| `create_document` | Create a markdown document |
| `search_knowledge` | Search knowledge base |
| `delegate_to_agent` | Hand off to a sub-agent |
| `ask_agent` | Quick question to a sub-agent |
| `recall_memory` | FTS5 search of memories |
| `save_memory` | Save fact/decision/commitment/person |
| `generate_image` | Generate image via Gemini API |
| `send_email` | Send email via SMTP |
| `send_telegram` | Send to linked Telegram chat |
| `create_calendar_event` | Create calendar event |
| `list_calendar_events` | List upcoming events |
| `update_calendar_event` | Modify event |
| `delete_calendar_event` | Cancel event |
| `web_search` | Search the internet |
| `search_x` | Search X/Twitter via Grok xAI |
| `fetch_url` | Fetch and extract URL content |
| `trace_knowledge` | Look up provenance of something Brain knows |

MCP tools added dynamically from connected external servers.

### 4.4 Memory Extraction

**Rule-based** (zero-LLM, every message):
- Regex patterns for decisions (`we decided`, `let's go with`...), commitments (`I'll`, `I will`...), people (`@name is/handles/works on`...)
- Saves with confidence 0.8, source `"rule"`
- Fuzzy dedup: skip if >80% word overlap with existing

**LLM extraction** (every 15 messages per channel):
- Extracts `decision`, `commitment`, `policy`, `person` types with confidence scores
- Filter: "Would someone search for this 3 months from now?"

**Memory consolidation** (periodic LLM pass):
- Dedup -> supersede old with `superseded_by`
- Detect outdated facts -> set `valid_until`
- Generate `insight` memories from cross-memory patterns

**Memory types:** `fact` (0.5), `decision` (0.8), `commitment` (0.7), `policy` (0.8), `person` (0.6), `insight` (0.7)

### 4.5 Knowledge Provenance

Each knowledge article stores `source_type`, `source_name`, `source_url`, `created_by`, `created_at`. The `trace_knowledge` tool surfaces attribution. Memories store `source_channel` and `source_message_id` linking to the original message.

### 4.6 Knowledge Base

Three ingestion paths: manual text, file upload (PDF extraction), URL import. Below 5000 tokens all articles are in system prompt; above that, keyword or Qdrant semantic search.

### 4.7 Skills System

Markdown files with YAML frontmatter at `workspaces/{slug}/brain/skills/`. Metadata: name, description, trigger (mention/schedule/keyword), channels, roles, autonomy.

15 bundled skills: daily-standup, meeting-notes, decision-logger, new-hire-buddy, client-onboarding, deal-tracker, support-triage, campaign-manager, content-calendar, retro-facilitator, proposal-tracker, invoice-reminder, competitive-intel, hiring-pipeline, release-notes.

### 4.8 Heartbeat Scheduler

`HEARTBEAT.md` defines cron-like schedules (`daily HH:MM`, `weekly {day} HH:MM`, `hourly :MM`). Server cron fires every minute and triggers Brain completions for matching schedules.

### 4.9 Reflection Cycles

Daily/weekly reflection: aggregate metrics -> LLM analysis -> written to `REFLECTIONS.md` + `reflection_history` table. Included in Brain's system prompt for self-awareness.

### 4.10 Sub-Agents

Custom AI agents with full personality: role, goal, backstory, instructions, constraints, escalation prompt. Own model, temperature, tool allowlist, trigger modes (mention/always/all). Per-agent skills. Agent templates available (Sales, Support, Meeting Scribe, etc.) or AI-generated from a description.

### 4.11 Living Briefs

AI-generated intelligence reports: scheduled or on-demand, shareable via public URL, template system.

### 4.12 Social Pulse

Social media intelligence: topic + query -> Grok xAI or web search -> sentiment score, themes, key posts, recommendations, predictions, risks.

### 4.13 LLM Usage Tracking

All LLM calls tracked: model, tokens, cost (USD), action type, channel, member. Admin dashboard shows per-workspace spend.

---

## 5. API Endpoints

All workspace endpoints: `/api/workspaces/{slug}/...`. Auth via JWT.

### Auth
- `POST /api/auth/register` — Register
- `POST /api/auth/login` — Login
- `GET /api/auth/config` — Auth mode
- `POST /api/auth/forgot` — Request reset
- `POST /api/auth/reset` — Reset password
- `POST /api/auth/verify` — Verify email
- `GET /api/auth/me` — Current user
- `PUT /api/auth/me` — Update profile
- `PUT /api/auth/me/password` — Change password
- `GET /api/auth/workspaces` — List workspaces
- `POST /api/auth/switch-workspace` — Switch workspace token

### Workspaces
- `POST /api/workspaces` — Create
- `POST /api/workspaces/{slug}/join` — Join
- `POST /api/join` — Join by code
- `GET /api/workspaces/{slug}` — Details
- `GET /api/workspaces/{slug}/info` — Info + plan
- `POST /api/workspaces/{slug}/invite` — Create invite link
- `POST /api/workspaces/{slug}/invite/email` — Email invite
- `GET /api/workspaces/{slug}/search` — Full-text search
- `GET /api/workspaces/{slug}/export` — Export JSON
- `POST /api/workspaces/import` — Import JSON
- `DELETE /api/workspaces/{slug}/destroy` — Destroy
- `GET /api/workspaces/{slug}/usage` — LLM usage stats

### Channels
- `GET/POST /api/workspaces/{slug}/channels` — List/create
- `DELETE /api/workspaces/{slug}/channels/{id}` — Delete
- `GET /api/workspaces/{slug}/channels/{id}/messages` — History
- `GET /api/workspaces/{slug}/channels/{id}/messages/{id}/thread` — Thread
- `PUT /api/workspaces/{slug}/channels/{id}/favorite` — Toggle favorite

### Members
- `GET /api/roles` — List roles
- `GET /api/workspaces/{slug}/members/{id}` — Get member
- `PUT /api/workspaces/{slug}/members/role` — Update role
- `PUT /api/workspaces/{slug}/members/permission` — Override permission
- `DELETE /api/workspaces/{slug}/members/{id}` — Kick
- `PUT /api/workspaces/{slug}/members/{id}/profile` — Update profile

### Tasks
- `POST/GET /api/workspaces/{slug}/tasks` — Create/list
- `GET/PUT/DELETE /api/workspaces/{slug}/tasks/{id}` — Get/update/delete

### Documents
- `POST/GET /api/workspaces/{slug}/documents` — Create/list
- `GET/PUT/DELETE /api/workspaces/{slug}/documents/{id}` — Get/update/delete

### Files & Folders
- `POST /api/workspaces/{slug}/channels/{id}/files` — Upload to channel
- `POST /api/workspaces/{slug}/folders/{id}/files` — Upload to folder
- `GET /api/workspaces/{slug}/files` — List
- `GET /api/workspaces/{slug}/files/{hash}` — Download (public)
- `PUT /api/workspaces/{slug}/files/{id}/update` — Rename
- `PUT /api/workspaces/{slug}/files/{id}/move` — Move
- `DELETE /api/workspaces/{slug}/files/{id}/delete` — Delete
- `POST /api/workspaces/{slug}/files/{id}/duplicate` — Duplicate
- `POST/GET/PUT/DELETE /api/workspaces/{slug}/folders[/{id}]` — Folder CRUD

### Brain
- `GET/PUT /api/workspaces/{slug}/brain/settings` — Settings
- `GET/PUT /api/workspaces/{slug}/brain/definitions/{file}` — Definition files
- `GET /api/workspaces/{slug}/brain/prompt` — Compiled system prompt
- `GET /api/workspaces/{slug}/brain/tools` — Available tools
- `POST /api/workspaces/{slug}/brain/execute-tool` — Execute tool directly
- `POST /api/workspaces/{slug}/brain/welcome` — Post welcome message
- `GET /api/workspaces/{slug}/brain/memories` — List memories
- `DELETE /api/workspaces/{slug}/brain/memories[/{id}]` — Delete memory/all
- `POST /api/workspaces/{slug}/brain/memories/pin` — Pin memory
- `POST /api/workspaces/{slug}/brain/memories/extract` — Trigger extraction
- `POST /api/workspaces/{slug}/brain/reflect` — Trigger reflection
- `GET /api/workspaces/{slug}/brain/reflections` — Reflection history
- `GET /api/workspaces/{slug}/brain/actions` — Action log
- `GET/POST/PUT/DELETE /api/workspaces/{slug}/brain/skills/{file}` — Skill CRUD
- `PUT /api/workspaces/{slug}/brain/skills/{file}/toggle` — Enable/disable
- `POST /api/workspaces/{slug}/brain/skills/generate` — AI-generate skill
- `POST/GET/PUT/DELETE /api/workspaces/{slug}/brain/knowledge[/{id}]` — Knowledge CRUD
- `POST /api/workspaces/{slug}/brain/knowledge/upload` — Upload file
- `POST /api/workspaces/{slug}/brain/knowledge/import-url` — Import URL

### Agents
- `POST/GET/PUT/DELETE /api/workspaces/{slug}/agents[/{id}]` — CRUD
- `GET /api/workspaces/{slug}/agents/templates` — Templates
- `POST /api/workspaces/{slug}/agents/generate` — AI-generate config
- `POST /api/workspaces/{slug}/agents/from-template` — Create from template
- `GET/PUT/DELETE /api/workspaces/{slug}/agents/{id}/skills/{file}` — Agent skills

### Calendar
- `POST/GET /api/workspaces/{slug}/calendar/events` — Create/list
- `GET/PUT/DELETE /api/workspaces/{slug}/calendar/events/{id}` — Get/update/delete

### Integrations
- `POST /w/{slug}/hook/{token}` — Incoming webhook
- `POST/GET/DELETE /api/workspaces/{slug}/brain/webhooks[/{id}]` — Webhook management
- `POST /api/telegram/{slug}/update` — Telegram webhook
- `GET/POST/PUT/DELETE /api/workspaces/{slug}/mcp-servers[/{id}]` — MCP servers

### Living Briefs
- `GET/POST /api/workspaces/{slug}/briefs` — List/create
- `GET/DELETE /api/workspaces/{slug}/briefs/{id}` — Get/delete
- `POST /api/workspaces/{slug}/briefs/{id}/generate` — Generate content
- `POST/DELETE /api/workspaces/{slug}/briefs/{id}/share` — Public share
- `GET /api/briefs/public/{token}` — View shared brief

### Social Pulse
- `POST/GET/DELETE /api/workspaces/{slug}/social-pulse[/{id}]` — CRUD

### Superadmin
- `GET /api/admin/stats` — Platform stats
- `GET /api/admin/workspaces` — All workspaces
- `GET /api/admin/accounts` — All accounts
- `PUT /api/admin/workspaces/{slug}/suspend` — Suspend
- `DELETE /api/admin/workspaces/{slug}` — Delete
- `PUT /api/admin/accounts/{id}/ban` — Ban
- `POST /api/admin/impersonate` — Impersonate
- `GET /api/admin/waitlist` — Waitlist entries

### System
- `GET /health` — Health check
- `GET /metrics` — Prometheus metrics
- `GET /ws` — WebSocket
- `POST /api/waitlist` — Join waitlist

---

## 6. Monetization

### Free Tier (default)
- Unlimited workspaces
- **5 members per workspace** (agents/Brain excluded)
- All features available
- Enforced in Go binary for all deployments

### Pro Tier
- Unlimited members
- Activated by `NEXUS_LICENSE` env var or `license_key` in config
- Binary unlock model (no per-seat pricing)

### Waitlist
- `POST /api/waitlist` (public) — email + plan
- `GET /api/admin/waitlist` (superadmin) — view entries

---

## 7. Tech Stack

### Backend (Go)
| Component | Library |
|-----------|---------|
| HTTP server | `net/http` stdlib |
| WebSocket | `nhooyr.io/websocket` |
| SQLite | `mattn/go-sqlite3` (CGO) |
| JWT | `golang-jwt/jwt/v5` (HS256) |
| Bcrypt | `golang.org/x/crypto/bcrypt` |
| Auto-TLS | `golang.org/x/crypto/acme/autocert` |
| Config | `BurntSushi/toml` |
| Logging | `rs/zerolog` |
| Metrics | `prometheus/client_golang` |
| Task queue | `hibiken/asynq` (Redis, goroutine fallback) |
| Full-text search | `blevesearch/bleve/v2` |
| Vector search | `qdrant/go-client` (optional) |
| MCP | `modelcontextprotocol/go-sdk` |
| Cron | `robfig/cron/v3` |
| PDF | `ledongthuc/pdf` |

### Frontend (SvelteKit 2)
| Component | Library |
|-----------|---------|
| Framework | SvelteKit 2, Svelte 5, static adapter |
| Rich text | Tiptap (ProseMirror) |
| Org chart | D3.js + d3-org-chart |
| Build | Vite |

### LLM / AI
| Service | Purpose |
|---------|---------|
| OpenRouter | Primary LLM provider |
| Google Gemini | Image generation |
| xAI Grok | X/Twitter + web search |
| OpenAI embeddings | Knowledge vectorization (via OpenRouter) |

### Data Directory
```
~/.nexus/
├── nexus.toml
├── nexus.db
└── workspaces/{slug}/
    ├── workspace.db
    ├── blobs/{2-char}/{sha256-hash}
    └── brain/
        ├── SOUL.md, INSTRUCTIONS.md, TEAM.md
        ├── MEMORY.md, REFLECTIONS.md, HEARTBEAT.md
        ├── skills/*.md (15 bundled)
        └── agents/{id}/skills/
```
