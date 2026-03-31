# Nexus — Project Specification

**Version:** v0.1.0 · March 30, 2026
**Production:** https://nexusteams.dev
**Repository:** https://github.com/vortex-303/nexus
**Status:** Public beta — deployed, accepting users

---

## 1. Product Summary

Nexus is an AI-native team workspace where a persistent **Brain** is the central nervous system. It remembers conversations, extracts decisions, coordinates agents and humans, and executes real actions — all in a single Go binary you can self-host or use in the cloud.

**One-liner:** A shared AI brain for your team — instant, private, self-hosted.

**Target users:** Privacy-conscious teams (legal, healthcare, finance, EU/GDPR), startups wanting AI-first collaboration, and developers who want to own their stack.

**Key differentiators:**
- Brain with persistent memory (not a stateless chatbot)
- Self-hosted with zero telemetry — data sovereignty by design
- Single 15MB binary — no Docker/Kubernetes required
- 200+ LLM models via OpenRouter, or run locally via Ollama
- ~100x cheaper than Slack AI ($0.10 vs $10/user/month)

---

## 2. Architecture

### 2.1 Stack

| Layer | Technology |
|-------|------------|
| Backend | Go 1.25, single binary (`//go:embed` SPA) |
| Database | SQLite WAL (1 global + 1 per workspace) |
| Frontend | SvelteKit 2 / Svelte 5, static adapter |
| Real-time | WebSocket hub per workspace (JSON envelope) |
| Auth | JWT HS256, 30-day expiry, 9 roles, 31 permissions |
| AI | OpenRouter (200+ models), Gemini (images), Ollama (local) |
| Search | Bleve full-text + optional Qdrant vector search |
| Task queue | asynq (Redis) with goroutine fallback |
| Metrics | Prometheus (`/metrics`) |
| MCP | Model Context Protocol (stdio + SSE) |

### 2.2 Deployment

| Method | Details |
|--------|---------|
| **Nexus Cloud** | `nexusteams.dev` — create workspace, instant |
| **Install script** | `curl -fsSL .../install.sh \| sh` → Linux amd64/arm64 |
| **Docker** | `ghcr.io/vortex-303/nexus:0.1.0` |
| **Fly.io** | `fly launch --from github.com/vortex-303/nexus` |
| **Source** | `make build && ./nexus serve` |

Current production: Fly.io `ewr` region, shared-cpu-1x, 512MB RAM, `nexus_data` volume.

### 2.3 Data Directory

```
~/.nexus/
├── nexus.toml                    # Config (TOML + env vars + CLI flags)
├── nexus.db                      # Global DB (accounts, workspaces)
└── workspaces/{slug}/
    ├── workspace.db              # Per-workspace DB (messages, tasks, members...)
    ├── blobs/{prefix}/{sha256}   # Content-addressed file storage
    └── brain/
        ├── SOUL.md, INSTRUCTIONS.md, TEAM.md
        ├── MEMORY.md, REFLECTIONS.md, HEARTBEAT.md
        ├── skills/*.md
        └── agents/{id}/skills/
```

### 2.4 Database Schema

**Global DB** (9 tables): `accounts`, `workspaces`, `sessions`, `invite_tokens`, `jwt_secrets`, `admin_audit_log`, `platform_announcements`, `platform_models`, `email_verifications`

**Per-Workspace DB** (25 tables): `members`, `channels`, `channel_members`, `messages`, `reactions`, `channel_reads`, `permission_overrides`, `guest_channels`, `tasks`, `files`, `folders`, `documents`, `brain_settings`, `brain_action_log`, `brain_memories`, `brain_channel_summaries`, `brain_knowledge`, `agents`, `org_roles`, `webhook_hooks`, `webhook_events`, `channel_integrations`, `email_threads`, `mcp_servers`, `notifications`, `notification_preferences`, `pinned_messages`

---

## 3. Feature Inventory

### 3.1 Messaging — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| Channels | Done | Public, private, with channel members |
| Direct Messages | Done | `dm-{id1}-{id2}` naming, auto-subscribe |
| Threaded replies | Done | `parent_id` on messages, Brain auto-follows threads |
| Markdown | Done | Full markdown with syntax highlighting |
| File upload | Done | Drag-drop, 50MB limit, content-addressed blobs |
| Typing indicators | Done | Real-time via WebSocket |
| @Mentions | Done | Autocomplete, auto-add to channel |
| Unread badges | Done | Per-channel count, tab title `(N) Nexus`, sort to top |
| Infinite scroll | Done | IntersectionObserver, cursor-based `?before=` |
| Pin to channel | Done | Thumbtack toggle in header, pins panel, click-to-scroll |
| Pin to memory | Done | 4 types: decision/commitment/policy/person |
| Desktop notifications | Done | Notification API with permission request |
| Channel favorites | Done | Star toggle, favorites section in sidebar |

### 3.2 Brain AI — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| @Brain mentions | Done | Any channel, auto-responds |
| Brain DM | Done | Private conversation with Brain |
| Tool calling | Done | 2-round loop: LLM → tools → results → response |
| Memory extraction | Done | Rule-based (zero cost) + LLM (every 15 messages) |
| Memory types | Done | fact, decision, commitment, policy, person, insight |
| Knowledge base | Done | Text, file (PDF), URL import, semantic search |
| Skills | Done | 15 bundled, YAML frontmatter, enable/disable, AI generation |
| Heartbeat | Done | Cron-like scheduler for daily/weekly Brain actions |
| Self-reflection | Done | Daily/weekly analysis → REFLECTIONS.md |
| Knowledge provenance | Done | Source attribution on all knowledge |
| 22 built-in tools | Done | Tasks, calendar, search, email, Telegram, web, images |
| MCP tools | Done | stdio + SSE, 15+ templates, auto-reconnect |

### 3.3 Agents — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| Custom agents | Done | Full CRUD, personality, model, tools, trigger modes |
| 9 templates | Done | Sales, Support, Meeting Scribe, etc. |
| AI config generation | Done | Describe in natural language → agent config |
| Per-agent skills | Done | Markdown skills scoped to agent |
| Agent delegation | Done | Brain delegates to sub-agents |
| Built-in agents | Done | Creative Director (image gen), Caly (exec assistant) |
| Agent DMs | Done | Private conversation with any agent |

### 3.4 Tasks — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| CRUD | Done | Create, read, update, delete |
| Board view | Done | Kanban columns by status |
| List view | Done | Table with sorting and filters |
| Statuses | Done | backlog, todo, in_progress, done, cancelled |
| Priorities | Done | low, medium, high, urgent |
| Assignment | Done | Assign to humans or agents |
| Due dates | Done | With approaching deadline follow-ups |
| Tags | Done | Free-form tags |
| Task scheduler | Done | Recurring tasks with RRULE, auto-assignment |
| Channel linking | Done | Link task to originating message |

### 3.5 Notifications — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| Bell icon inbox | Done | Sidebar header, unread badge |
| Dropdown panel | Done | Tabs: All / Mentions / DMs |
| @mention trigger | Done | Notifies mentioned user |
| DM trigger | Done | Notifies DM recipient |
| Task assign trigger | Done | Notifies assignee |
| Welcome notification | Done | On workspace create and member join |
| Desktop notification | Done | Notification API when tab hidden |
| Mark read | Done | Individual and mark-all-read |
| Click to navigate | Done | Opens source channel + message |

### 3.6 Platform & Admin — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| Auth | Done | Email/password, JWT, login page, invite links/codes |
| Superadmin | Done | `SUPERADMIN_EMAIL` env var, `nexus admin promote` CLI |
| Admin panel | Done | Stats, workspace management, model curation, audit log |
| Org chart | Done | D3 hierarchy, drag-drop, role slots, profiles |
| RBAC | Done | 9 roles, 31 permissions, per-member overrides |
| License keys | Done | Ed25519 signed, enforces member limits |
| Webhooks | Done | Token-auth inbound, event log, Brain processing |
| Email integration | Done | Inbound SMTP, thread tracking, outbound replies |
| Telegram | Done | Bot webhook, auto-channel, Brain responses |
| Workspace export | Done | Full JSON export, import, destroy with kill switch |
| Network transparency | Done | Log of all outbound connections |
| LLM usage tracking | Done | Per-model token/cost dashboard |

### 3.7 Frontend — SHIPPED

| Feature | Status | Details |
|---------|--------|---------|
| Chat header toolbar | Done | Horizontal pills: online/pinned/members with active states |
| Toast notifications | Done | Global system, success/error/info |
| Landing page | Done | Standalone HTML, dark theme, animated stats |
| i18n | Done | EN/ES toggle, 271 translated elements, localStorage |
| Model browser | Done | OpenRouter catalog, admin-pinned models |
| Search | Done | Full-text search across messages/tasks/docs |

---

## 4. Known Gaps

### 4.1 UX Gaps (backend exists, frontend missing)

| Gap | Backend | What's Missing |
|-----|---------|----------------|
| Emoji picker | `sendReaction`/`removeReaction` work | No picker UI — reactions render if they exist |
| Message editing | `message.edit` WS handler works | No edit button or inline edit UI |
| Streaming responses | `CompleteStream` in openrouter.go | No SSE handler — responses appear all at once |
| Task detail modal | Full task fields in API | No description/comments/assignee picker UI |

### 4.2 Not Yet Built

| Feature | Priority | Complexity |
|---------|----------|------------|
| Notification preferences | P2 | Small — table exists, need UI |
| Browser Web Push (VAPID) | P2 | Medium |
| Mobile responsive + PWA | P3 | Large |
| "Catch Me Up" AI summaries | P3 | Medium |
| Collaborative editing (Y.js) | P3 | Large |
| E2E encryption (MLS) | P4 | Large |
| SSO / SAML / OAuth | P4 | Medium |
| Local LLM (llama.cpp) | P4 | Medium |
| Document RAG | P4 | Medium |

---

## 5. API Surface

All workspace endpoints: `GET/POST/PUT/DELETE /api/workspaces/{slug}/...`
Auth via `Authorization: Bearer <jwt>` or `?token=` for WebSocket.

| Group | Endpoints | Count |
|-------|-----------|-------|
| Auth | register, login, config, forgot, reset, verify, me, workspaces, switch | 11 |
| Workspaces | create, join, info, invite, search, export, import, destroy, usage | 10 |
| Channels | list, create, delete, messages, thread, favorite, browse, join, leave, members, pin/unpin, pins | 14 |
| Members | get, role, permission, kick, profile | 5 |
| Tasks | create, list, get, update, delete, runs | 6 |
| Documents | create, list, get, update, delete | 5 |
| Files | upload, list, download, rename, move, delete, duplicate, folders | 10 |
| Brain | settings, definitions, prompt, tools, welcome, memories, knowledge, skills, actions, heartbeat, reflection | 20+ |
| Agents | create, list, update, delete, templates, generate, skills | 10 |
| Notifications | list, read, read-all, count | 4 |
| Admin | accounts, workspaces, models, audit log, announcements, waitlist | 15+ |
| WebSocket | 30+ event types (message.new, typing, presence, task.*, notification.new, etc.) | 30+ |

**Total: ~140+ endpoints**

---

## 6. Monetization

| Tier | Price | Target | Limits |
|------|-------|--------|--------|
| **Free** | $0 forever | Evaluation, small teams | 5 members/workspace |
| **Pro** | $29/mo flat | Growing teams | Unlimited members |
| **Self-Hosted** | $0 | Privacy-first | 5 members (free), unlimited (Pro license) |

License enforcement: Ed25519 signed keys, checked in Go binary. Pro unlocked via `NEXUS_LICENSE` env var.

---

## 7. Release History

| Version | Date | Highlights |
|---------|------|------------|
| **v0.1.0** | 2026-03-30 | First public release. Notifications, unread badges, infinite scroll, i18n, superadmin CLI, header toolbar redesign, welcome notifications, landing page fixes |

---

## 8. Development

### Commands

```bash
make dev              # Build web + Go, run on :3000
make build            # Production binary
cd web && npm run build  # Frontend only
go build ./cmd/nexus/    # Backend only
fly deploy            # Deploy to Fly.io
nexus admin promote <email>  # Promote to superadmin
nexus db global "SELECT ..."  # Query global DB
nexus db <slug> "SELECT ..."  # Query workspace DB
```

### Conventions

- **Go:** stdlib style, no frameworks. HTTP handlers are methods on `*Server`
- **Frontend:** Svelte 5 runes (`$state`, `$derived`), scoped `<style>` blocks
- **CSS:** Custom properties in `app.css`, dark theme, orange accent
- **DB:** Sequential migrations in `migrations.go` (currently v54)
- **Files:** Content-addressed by SHA-256 hash
- **WebSocket:** JSON envelope `{type, payload}` protocol
- **Brain:** System prompt assembled from definition files + memories + skills + knowledge + context

### Key Files

| File | Purpose |
|------|---------|
| `cmd/nexus/main.go` | CLI entry point |
| `internal/server/server.go` | Router, route registration |
| `internal/server/ws.go` | WebSocket hub, message handling |
| `internal/server/notifications.go` | Notification CRUD + helper |
| `internal/brain/brain.go` | Brain engine, OpenRouter, tool loop |
| `internal/db/migrations/migrations.go` | All SQLite migrations |
| `internal/hub/protocol.go` | WebSocket message types |
| `web/src/routes/(app)/w/[slug]/+page.svelte` | Main workspace page |
| `web/src/lib/api.ts` | REST API client (140+ functions) |
| `web/src/lib/ws.ts` | WebSocket client |
| `web/static/landing.html` | Standalone marketing page |

---

## 9. Roadmap

### Next (P1)
1. Emoji reaction picker UI
2. Message edit button + inline edit
3. Streaming Brain responses (SSE)
4. Cut v0.2.0 with these features

### Soon (P2)
5. Notification preferences UI
6. Browser Web Push (VAPID + Service Worker)
7. Thread reply notifications
8. Task detail modal with comments

### Later (P3-P4)
9. Mobile responsive + PWA
10. "Catch Me Up" AI channel summaries
11. Collaborative editing (Y.js)
12. Search improvements (channel-scoped, date range, person filter)
13. SSO/SAML, E2E encryption, local LLM, document RAG

---

*Last updated: 2026-03-30 · v0.1.0*
