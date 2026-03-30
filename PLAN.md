# Nexus — Product Spec, Architecture & Roadmap

> Last updated: 2026-03-04

## What Is Nexus

An AI-native team workspace — chat, tasks, docs, and a persistent AI Brain — in a single Go binary you own completely. Self-hosted or cloud. Privacy-first. No vendor lock-in.

**One-liner:** A shared AI brain for your team — instant, private, self-hosted.

---

## Core Architecture

```
┌──────────────────────────────────────────────────┐
│                  Go Binary (~15MB)               │
│                                                  │
│  ┌────────────┐  ┌──────────┐  ┌──────────────┐ │
│  │ HTTP Server │  │ WS Hub   │  │ SMTP Server  │ │
│  │ :8080      │  │ (per-ws) │  │ :2525        │ │
│  └─────┬──────┘  └────┬─────┘  └──────┬───────┘ │
│        │              │               │          │
│  ┌─────┴──────────────┴───────────────┴───────┐  │
│  │              Request Router                │  │
│  │  /api/* → REST  │  /ws → WebSocket        │  │
│  │  /* → SPA       │  /w/{slug}/hook → Wbhk  │  │
│  └────────┬───────────────────┬───────────────┘  │
│           │                   │                  │
│  ┌────────┴────────┐ ┌───────┴────────────────┐ │
│  │ Auth (JWT/HS256) │ │ Brain Engine           │ │
│  │ 9 roles, 31 perm│ │ OpenRouter + Gemini    │ │
│  └────────┬────────┘ │ Tools, Memory, Skills  │ │
│           │          │ Agent Runtime           │ │
│  ┌────────┴────────┐ │ MCP Client (stdio/SSE) │ │
│  │ SQLite (WAL)    │ │ Heartbeat Scheduler    │ │
│  │ 1 global DB     │ └────────────────────────┘ │
│  │ 1 DB per ws     │                            │
│  │ Blobs on disk   │                            │
│  └─────────────────┘                            │
│                                                  │
│  ┌──────────────────────────────────────────────┐│
│  │ Embedded SvelteKit SPA (//go:embed all:build)││
│  └──────────────────────────────────────────────┘│
└──────────────────────────────────────────────────┘
```

### Key Design Decisions

| Decision | Choice | Why |
|----------|--------|-----|
| Database | SQLite (WAL, per-workspace) | Zero config, single file, easy backup (`cp`) |
| Frontend embedding | `//go:embed all:build` | Single binary distribution, no file server |
| Auth | JWT HS256, 30-day expiry | Stateless, workspace-scoped tokens |
| LLM | OpenRouter (200+ models) | Model-agnostic, user brings their own key |
| Image gen | Google Gemini API | Native image generation, separate key |
| Real-time | WebSocket hub per workspace | Simple, no Redis/NATS dependency |
| File storage | Content-addressed (SHA-256) | Dedup, immutable URLs, local filesystem |
| Config | TOML file + env vars + CLI flags | Standard, layered override |
| Metrics | Prometheus (`/metrics`) | Industry standard, Grafana/Fly Metrics compatible |
| Task queue | asynq (Redis) | Persistent retries, dedup; goroutine fallback when no Redis |
| Vector search | Qdrant (gRPC) | Semantic knowledge search; SQL LIKE fallback when no Qdrant |
| Embeddings | OpenRouter (`text-embedding-3-small`) | 1536-dim, reuses existing API key |

---

## Current State — Feature Inventory

### Fully Working

| Feature | Details |
|---------|---------|
| **Chat** | Channels (public/private), DMs, markdown, reactions (backend only — no picker UI), file upload (50MB), typing indicators, @mention autocomplete, /slash commands |
| **Brain AI** | @Brain mentions, DM with Brain, 2-round tool calling, system prompt from 5 definition files + memories + skills + knowledge + summaries |
| **Memory extraction** | Auto-extracts facts/decisions/commitments/people every N messages via asynq task queue (Redis) with goroutine fallback, stores in DB, feeds into Brain context |
| **Knowledge base** | Text articles, file upload (.txt/.md/.pdf), URL import with preview, semantic vector search (Qdrant) with SQL LIKE fallback |
| **Brain skills** | Markdown files with YAML frontmatter (trigger/autonomy/roles), enable/disable, AI generation, templates |
| **Tasks** | Full CRUD, 5 statuses, 4 priorities, tags, assignee, due date, channel/message linking, real-time WS sync |
| **Documents** | Full CRUD, Tiptap rich editor (headings, lists, code blocks, images, checklists), auto-save, real-time WS sync |
| **Agents** | Full CRUD, 9 templates, AI-generated configs, per-agent skills, mention/always/all triggers, tool calling, image generation with skill-enriched prompts, delegation |
| **Built-in agents** | Creative Director (image gen), Caly (exec assistant) — auto-seeded per workspace |
| **MCP tools** | stdio + SSE transports, template catalog (15+ servers), per-workspace management, auto-reconnect, namespaced tools |
| **Roles & permissions** | 9 roles (admin→guest), 31 permissions, per-member overrides |
| **Org chart** | D3 hierarchy, drag-drop reparenting, role slots (vacant/filled), member profiles (title/bio/goals) |
| **Webhooks** | Token-authenticated inbound, event log, Brain processing with configurable autonomy |
| **Email** | Inbound SMTP server, thread tracking, auto-channel creation, outbound replies, configurable autonomy |
| **Telegram** | Bot webhook, auto-channel linking, Brain responses, autonomy settings |
| **File storage** | Content-addressed blobs, dedup, immutable cache headers, inline display for images/PDFs |
| **Platform admin** | Superadmin panel: stats, workspace management, account management, model curation, audit log, data export, announcements, impersonation |
| **Auth** | Anonymous workspace creation, email/password accounts, login, workspace switching (auto-scope for single-ws users), invite links, invite by email |
| **Heartbeat** | Cron-like scheduler for Brain actions (daily standup, weekly reports, etc.) parsed from HEARTBEAT.md |
| **Model browsing** | OpenRouter model catalog with 1h cache, admin-pinned models |

### Partially Implemented / UI Gaps

| Feature | What's Missing |
|---------|---------------|
| **Reactions** | Backend works, `sendReaction`/`removeReaction` exist in ws.ts, but **no emoji picker UI** — reactions render if they exist but users can't add them |
| **Message editing** | Backend handles `message.edit` WS event, but **no edit button/UI** in frontend |
| **Unread counts** | `channel_reads` table tracks last read, but **no unread badge** in sidebar |
| **Message pagination** | Only loads latest batch — **no infinite scroll / "load more"** |
| **Task detail view** | Board/list show title+status+priority but **no description, comments, or assignee** in UI |
| **Task drag-and-drop** | Kanban columns exist but **status change only via dropdown**, no DnD |
| **Due date editor** | Due dates display if set but **no date picker** in task creation form |
| **Role editing** | Roles tab displays the permission matrix but **no edit UI** |
| **Collaborative editing** | Documents are single-writer — **no OT/CRDT, no multi-cursor** |
| **Appearance/theme** | Preferences modal has "Coming soon" placeholder |
| **Brain permissions** | `brain.mention`/`brain.dm` permissions defined but **not enforced** |
| **Contact/CRM** | Permissions defined but **no endpoints or UI** |
| **Streaming responses** | `CompleteStream` implemented in openrouter.go but **no SSE handler** uses it |

### Not Implemented (Spec'd but not built)

| Feature | Spec Reference |
|---------|---------------|
| E2E encryption (MLS) | SPEC.md Phase 5 |
| SSO / SAML / OAuth | SPEC.md Phase 5 |
| Compliance exports | SPEC.md Phase 5 |
| Local LLM (llama.cpp) | SPEC.md Phase 4 — landing page claims "local Llama" |
| Native iOS/Mac apps | SPEC.md Phase 6 |
| Offline / CRDT sync | SPEC.md Phase 6 |
| Skill marketplace | SPEC.md Phase 6 |
| Document RAG (vector search) | SPEC.md Phase 3 — **knowledge base has semantic vector search (Qdrant), document RAG not yet implemented** |
| Sub-agent orchestration | SPEC.md Phase 3 — delegation exists but single-pass only |

---

## Infrastructure

### Current Cloud Deployment (Fly.io)

| Component | Value |
|-----------|-------|
| App | `nexus-workspace` on Fly.io |
| URL | https://nexus-workspace.fly.dev |
| Machine | `shared-cpu-1x`, 512MB RAM |
| Region | `ewr` (Newark, NJ), single region |
| Volume | `nexus_data` mounted at `/data` |
| Connections | Hard limit 100, soft limit 80 |
| Ports | 8080 (HTTP, TLS via Fly proxy), 2525→25 (SMTP) |
| Auto-scaling | min 1 machine, auto-stop on idle |
| Metrics | `GET /metrics` (Prometheus format) — scrape with Fly Metrics or Grafana Cloud |

### Optional External Services

All optional — Nexus runs fully standalone without any of these. Set env vars to enable:

| Service | Env Var | Purpose | Fallback |
|---------|---------|---------|----------|
| Redis (Upstash) | `REDIS_URL` | asynq task queue — persistent retries, dedup, rate limiting for memory extraction & summarization | Raw goroutines (no retry, no persistence) |
| Qdrant | `QDRANT_URL` | Vector search — semantic knowledge retrieval using 1536-dim embeddings | SQL `LIKE` keyword search |

**Setup:**
```bash
fly ext upstash redis create           # auto-sets REDIS_URL
fly secrets set QDRANT_URL=host:6334   # Qdrant Cloud free tier or self-hosted
```

### Scaling Ceiling

| Constraint | Impact | Mitigation |
|------------|--------|------------|
| Single machine + volume | Can't horizontally scale | Upgrade machine size; eventually LiteFS or Postgres for global DB |
| SQLite write serialization | Heavy agent activity queues writes (5s busy timeout) | Per-workspace isolation helps; heavy workspaces get their own lock |
| 100 WebSocket connection cap | ~100 simultaneous users across all workspaces | Increase in fly.toml; eventually multiple machines with shared state |
| 512MB RAM | MCP subprocesses (node/python) compete for memory | Upgrade to 1GB+; limit concurrent MCP servers |
| Local blob storage | No CDN, disk-bound | Move to S3/R2 for blobs |
| Single region | Latency for non-US users | Add regions (requires shared DB strategy) |

### Self-Hosted Setup

```bash
# Install (requires GitHub release to exist)
curl -fsSL https://raw.githubusercontent.com/vortex-303/nexus/main/install.sh | sh

# Run
nexus serve                          # http://localhost:8080, data in ~/.nexus/
nexus serve --data-dir /var/nexus    # Custom data directory
nexus serve --domain nexus.myco.com  # Auto-TLS via Let's Encrypt

# Docker
docker run -p 8080:8080 -v nexus_data:/data ghcr.io/vortex-303/nexus

# Build from source
git clone https://github.com/vortex-303/nexus.git
cd nexus && make dev                 # Builds web + Go, runs on :3000 (dev mode)
```

### Build & Release

| Tool | Config | What |
|------|--------|------|
| Make | `Makefile` | `build`, `web`, `dev`, `clean` |
| GoReleaser | `.goreleaser.yaml` | 4 targets (linux/darwin × amd64/arm64), checksums, GitHub release |
| CI | `.github/workflows/release.yml` | Tag push `v*` → goreleaser → GitHub release |
| Docker | `Dockerfile` | 3-stage build (node → go → alpine), includes node/python/uv for MCP |

**No GitHub releases exist yet.** The install script will fail until a `v*` tag is pushed and goreleaser runs.

---

## Pricing Strategy

### Recommended: Open Core + Per-Seat Cloud

| Tier | Price | Target | Includes |
|------|-------|--------|----------|
| **Self-Hosted** | Free forever | Developers, small teams | Full product, unlimited users, AGPL |
| **Cloud Free** | $0 | Evaluation | 3 users, 1 workspace, 10GB storage |
| **Cloud Pro** | $8/user/mo | Small teams (3-50) | Unlimited users, agents, 100GB storage, custom domain |
| **Cloud Business** | $18/user/mo | Growing teams (50-500) | + SSO/SAML, audit logs, RBAC, priority support |
| **Enterprise** | Custom | Regulated / 500+ | Air-gapped, SLA, compliance exports, custom data residency |

### What Gets Gated (paid only)

- SSO / SAML — IT/security buyer
- Audit logs & compliance exports — legal buyer
- Advanced RBAC / org-wide admin — manager buyer
- AI token quota overages — usage-based add-on
- Priority support with SLA

### What Stays Free Forever

- All core features (chat, tasks, docs, Brain, agents, MCP)
- Self-hosted = full product, no feature locks
- API and webhooks
- Local LLM support (when implemented)

### Competitive Reference

| Product | Price | Self-Hosted |
|---------|-------|-------------|
| Slack | $7.25/user/mo | No |
| Notion | $10/user/mo | No |
| Linear | $8/user/mo | No |
| Mattermost | $10/user/mo | Free (open core) |
| Plane | $6/user/mo | Free (AGPL) |
| **Nexus** | **$8/user/mo** | **Free (full product)** |

---

## What's Missing — Next Steps

### P0: Ship-Blockers (before anyone can install)

- [ ] **Cut first GitHub release** — `git tag v0.1.0 && git push --tags` → goreleaser creates binaries → install.sh works
- [ ] **README.md** — what it is, screenshot, install instructions, link to docs
- [ ] **LICENSE** — AGPL-3.0 (matches open-core strategy, same as Plane/Cal.com)
- [ ] **CLAUDE.md** — project conventions for AI-assisted development
- [ ] **Remove hardcoded superadmin** — `nruggieri@gmail.com` is baked into migrations.go; needs to be configurable or use first-user-is-admin pattern
- [ ] **Fix install.sh exit message** — should print the URL (`http://localhost:8080`) not just `nexus serve`

### P1: Core UX Gaps (next 2-4 weeks)

- [ ] **Emoji reaction picker** — the backend works, ws.ts has the functions, just need the UI
- [ ] **Message edit button** — context menu or hover action to edit own messages
- [ ] **Unread badges** — channel sidebar shows unread count from `channel_reads` table
- [ ] **Message pagination** — infinite scroll or "Load older messages" button
- [ ] **Task detail modal** — description editor, assignee picker, due date picker, comments
- [ ] **Task drag-and-drop** — board view column DnD for status changes
- [ ] **Streaming Brain responses** — use `CompleteStream` + SSE for real-time typing effect

### P2: Landing Page Claims Gap (what we advertise but don't have)

| Claim | Reality | Fix |
|-------|---------|-----|
| "Run models locally with llama.cpp" | Not implemented | Add local LLM integration or remove claim |
| "8 templates" for agents | 9 templates exist + 2 built-in agents | Accurate, keep |
| "Back it up with `cp`" | True for SQLite | Accurate, keep |
| "5MB binary" | Binary is ~15MB | Update landing page |
| "Deploy in 30 seconds" | True if release exists | Need to cut first release |

### P3: Monetization Infrastructure (before charging)

- [ ] Workspace user limits (enforce 3-user cap on free tier)
- [ ] Storage quotas (track blob usage per workspace)
- [ ] Usage metering (token count per Brain/agent call)
- [ ] Stripe integration for billing
- [ ] License key system for self-hosted enterprise
- [ ] SSO/SAML (gated feature for Business tier)
- [ ] Audit log export (gated feature for Business tier)

### P4: Scale Preparation

- [ ] Upgrade Fly machine (2 CPU, 2GB RAM minimum)
- [ ] Raise connection limits to 1000+
- [ ] Add S3/R2 blob storage (keep local as fallback)
- [ ] Add CDN for static assets
- [ ] Database connection pooling (`SetMaxOpenConns`)
- [ ] Rate limiting on public endpoints (create workspace, login)

### P5: Product Maturity

- [ ] Collaborative document editing (Tiptap + Y.js or CRDT)
- [ ] Thread support in chat (reply-to-message)
- [ ] Search across all messages/docs/tasks
- [ ] Notification system (in-app + email digest)
- [ ] Mobile-responsive workspace UI (currently desktop-optimized)
- [ ] API documentation (OpenAPI spec)
- [ ] Local LLM support (llama.cpp or Ollama)
- [ ] Document versioning / history
- [ ] Data import from Slack/Discord/Notion

---

## Directory Structure

```
nexus/
├── cmd/nexus/main.go          # CLI entry point (serve, version)
├── internal/
│   ├── auth/                  # JWT claims, middleware
│   ├── brain/                 # Brain engine, OpenRouter, Gemini, memory, skills, tools
│   │   └── skills/            # Built-in skill templates + agent skills
│   ├── config/                # Config loading (TOML + env + flags)
│   ├── db/                    # SQLite setup, migrations (global + per-workspace)
│   ├── hub/                   # WebSocket hub, protocol types
│   ├── id/                    # ID generation (ULID, slug, short)
│   ├── mcp/                   # MCP client manager (stdio + SSE)
│   ├── metrics/               # Prometheus metric definitions (promauto)
│   ├── roles/                 # RBAC: 9 roles, 31 permissions, checker
│   ├── vectorstore/           # Qdrant gRPC wrapper (upsert, search, delete)
│   └── server/                # HTTP handlers, WS handlers, SPA serving
│       ├── server.go          # Router, SPA handler, cache headers
│       ├── auth.go            # Login, register, switch-workspace
│       ├── workspace.go       # Create/join/invite workspace
│       ├── ws.go              # WebSocket hub, message handling
│       ├── channels.go        # Channel CRUD
│       ├── tasks.go           # Task CRUD
│       ├── documents.go       # Document CRUD
│       ├── files.go           # File upload/download (content-addressed)
│       ├── brain.go           # Brain settings, definitions, trigger handler
│       ├── brain_tools.go     # Built-in tool implementations
│       ├── brain_memory.go    # Memory CRUD, extraction trigger
│       ├── brain_knowledge.go # Knowledge base CRUD, URL import, vector embedding
│       ├── taskqueue.go       # asynq task queue (Redis) with goroutine fallback
│       ├── brain_skills.go    # Brain skill management
│       ├── brain_heartbeat.go # Cron-like heartbeat scheduler
│       ├── agents.go          # Agent CRUD, templates, runtime
│       ├── agent_runtime.go   # Agent mention/trigger execution
│       ├── org_chart.go       # Org chart + role management
│       ├── members.go         # Member management, permissions
│       ├── permissions.go     # Permission middleware
│       ├── webhooks.go        # Inbound webhook processing
│       ├── email.go           # SMTP server + outbound email
│       ├── telegram.go        # Telegram bot integration
│       ├── mcp_servers.go     # MCP server CRUD
│       ├── mcp_templates.go   # MCP template catalog
│       ├── models.go          # Model browsing, pinned models
│       └── admin.go           # Superadmin panel handlers
├── web/
│   ├── src/
│   │   ├── routes/
│   │   │   ├── (app)/+page.svelte          # Login/create workspace
│   │   │   ├── (app)/w/[slug]/+page.svelte # Main workspace (chat/tasks/notes/brain/team)
│   │   │   ├── (app)/workspaces/           # Workspace picker
│   │   │   └── (app)/admin/                # Superadmin panel
│   │   ├── lib/
│   │   │   ├── api.ts          # REST API client (70+ functions)
│   │   │   ├── ws.ts           # WebSocket client, auto-reconnect
│   │   │   ├── stores/         # Svelte stores (channels, members, messages)
│   │   │   ├── editor/         # TiptapEditor component
│   │   │   └── components/     # OrgChart component
│   │   └── app.css             # Design system (CSS custom properties)
│   ├── static/
│   │   └── landing.html        # Standalone marketing page
│   └── embed.go                # //go:embed all:build
├── install.sh                  # Curl-pipe installer
├── Dockerfile                  # 3-stage build
├── fly.toml                    # Fly.io deployment config
├── .goreleaser.yaml            # Cross-platform release builds
├── .github/workflows/release.yml # Tag → goreleaser → GitHub release
├── Makefile                    # build, web, dev, clean
├── PLAN.md                     # This file
├── SPEC.md                     # Product vision & specification
└── ARCHITECTURE.md             # Technical architecture reference
```

---

## Database Schema Summary

### Global DB (`nexus.db`) — 8 tables
`accounts`, `workspaces`, `sessions`, `invite_tokens`, `jwt_secrets`, `admin_audit_log`, `platform_announcements`, `platform_models`

### Per-Workspace DB (`workspaces/{slug}/workspace.db`) — 21 tables
`members`, `channels`, `messages`, `reactions`, `channel_reads`, `permission_overrides`, `guest_channels`, `tasks`, `files`, `documents`, `brain_settings`, `brain_action_log`, `brain_memories`, `brain_channel_summaries`, `brain_knowledge`, `agents`, `org_roles`, `webhook_hooks`, `webhook_events`, `channel_integrations`, `email_threads`, `mcp_servers`

---

## Verification Checklist

To verify the current product works end-to-end:

1. `make dev` → opens at http://localhost:3000
2. Create workspace with name "Test Team" and your display name → lands in `/w/<slug>`
3. Send a message in #general → appears in real-time
4. Upload a file → appears inline if image, download link otherwise
5. Create a task from board view → appears on kanban
6. Create a document → rich editor saves and syncs
7. Configure Brain (Brain tab → Settings → add OpenRouter API key)
8. @Brain in chat → responds with tool calls if relevant
9. Invite someone via invite link → they join as member
10. Check org chart → hierarchy renders with D3
11. Add an MCP server (e.g., DuckDuckGo search) → tools appear in Brain's repertoire
12. Visit /landing.html → marketing page renders all sections
13. `curl localhost:3000/metrics | grep nexus_` → Prometheus metrics present
14. @Brain mention → `nexus_llm_calls_total` increments in metrics

---

# Next Features Roadmap (March 2026)

## 1. Pinned Messages [DONE]

- Migration v53: `pinned_messages` table
- Pin/unpin via hover action (star icon), orange "Pinned" tag on messages
- Pins button in channel header with count, opens Pins panel
- Click pinned message to scroll to it. Real-time sync via WebSocket.

## 2. Inbox & Notification System

### 2a. Data Model

**New table: `notifications`**
```sql
CREATE TABLE notifications (
  id TEXT PRIMARY KEY,
  workspace_slug TEXT NOT NULL,
  recipient_id TEXT NOT NULL,
  type TEXT NOT NULL,                 -- mention, dm, announcement, brief, system, thread_reply
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL DEFAULT '',      -- deep link: /w/{slug}?c={channelId}&m={messageId}
  source_id TEXT NOT NULL DEFAULT '',
  actor_id TEXT NOT NULL DEFAULT '',
  read BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TEXT NOT NULL
);
```

**New table: `notification_preferences`**
```sql
CREATE TABLE notification_preferences (
  member_id TEXT NOT NULL,
  channel_id TEXT NOT NULL DEFAULT '__global__',
  level TEXT NOT NULL DEFAULT 'mentions',  -- all, mentions, nothing
  browser_push BOOLEAN NOT NULL DEFAULT TRUE,
  mobile_push BOOLEAN NOT NULL DEFAULT TRUE,
  email_digest BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (member_id, channel_id)
);
```

### 2b. Notification Triggers

| Event | Recipients | Type |
|-------|-----------|------|
| @mention in channel | Mentioned user | `mention` |
| New DM | DM recipient | `dm` |
| Platform announcement (super admin) | All workspace owners | `announcement` |
| Workspace announcement (admin) | All workspace members | `announcement` |
| Brain weekly brief published | All members (configurable) | `brief` |
| Brain heartbeat fires | Channel members | `system` |
| Task assigned | Assignee | `system` |
| Thread reply | Thread participants | `thread_reply` |
| Invited to channel | Invited member | `system` |

### 2c. Delivery Channels

1. **In-app inbox** — bell icon, unread badge, dropdown list
2. **Browser push** — Web Push API (VAPID keys), Service Worker
3. **Mobile push** — PWA Service Worker (Chrome/Safari)
4. **Email digest** — Optional daily summary via SendGrid

### 2d. Inbox UI

- Bell icon in top-right with unread count badge
- Dropdown panel: tabs (All | Mentions | DMs | Announcements)
- Each entry: icon, title, preview, timestamp, read/unread
- Click navigates to source. "Mark all read" button.
- Settings page: global defaults + per-channel overrides

### 2e. Announcement System (Multi-Tier)

- **Super Admin → Owners**: extend `platform_announcements` to create notification rows
- **Admin → Team**: `POST /api/workspaces/{slug}/announcements`, notifies all members, pinned banner
- **Brain → Members**: brief published → notification with link, heartbeat → optional notification

### 2f. Implementation Order

1. `notifications` table + CRUD endpoints
2. In-app inbox UI (bell icon, dropdown, unread badge)
3. Trigger hooks in handlers (mention, DM, thread, etc.)
4. Browser Web Push (VAPID, Service Worker)
5. `notification_preferences` + settings UI
6. Workspace announcements endpoint + banner
7. Email digest (daily cron, SendGrid)

## 3. Search Improvements

### Current: Bleve full-text, workspace-wide, type filtering, recency bias

### Enhancements

- **Channel-scoped**: `channel_id` filter, default "This channel" when searching from a channel
- **Person filter**: `sender` param, "From: @person" pill
- **Date range**: `after`/`before` params, quick presets (Today, Past week, Past month)
- **Thread search**: index with parent_id, show thread context
- **Slash command**: `/search query` opens SearchModal pre-filled

### Implementation
1. Backend: Add filter params to search handler, compound Bleve queries
2. Frontend: Filter pills in SearchModal, context-aware defaults

## 4. Mobile Responsive Design

### Strategy: Progressive enhancement, don't break desktop

### Breakpoints
- `>1024px`: Full desktop (sidebar + chat + drawer)
- `768-1024px`: Tablet — collapsible sidebar
- `<768px`: Mobile — bottom nav, full-screen panels

### Mobile Layout
- **Bottom nav**: Chat, DMs, Pages, Activity, Profile
- Channel list → full screen, tap → chat view full screen
- Back arrow to return, swipe right → channel list, swipe left → members
- Thread → full screen overlay

### Component Changes
- Sidebar: `position: fixed` overlay, hamburger toggle
- Modals: full-screen on mobile
- Message actions: long press instead of hover
- Emoji picker: bottom sheet

### PWA
- `manifest.json` with icons, `display: standalone`
- Service Worker for offline shell + push notifications

### Order
1. Viewport meta + CSS breakpoints
2. Sidebar responsive (collapsible 768px, hamburger <768px)
3. Chat view full-screen mobile + back nav
4. Bottom nav bar
5. Touch interactions (long press, swipe)
6. PWA manifest + Service Worker

## 5. "Catch Me Up" — Brain Channel Summaries

### Concept
When opening a channel with 5+ unreads, show a "Catch me up" button that generates an AI TL;DR of missed messages.

### UX

**Unread banner** (above message list):
```
42 new messages since yesterday
[Catch me up]  [Jump to new]
```

**Also**: `/catchup` slash command, inbox "Catch up on all" button

**Summary output** — ephemeral card (not persisted), structured:
- Key decisions made
- Action items / tasks mentioned
- Open questions
- Important links/files shared
- Most active participants

### Backend

**Endpoint**: `POST /api/workspaces/{slug}/channels/{channelID}/catchup`
1. Get `last_read_at` from `channel_reads`
2. Fetch messages since then (cap 200)
3. Build context: content, senders, threads, reactions
4. Call LLM with summary system prompt
5. Return structured JSON

**Caching**: Use `brain_channel_summaries` table. Cache key = channel_id + message_count + last_message_id. Invalidate on new messages.

### Frontend
1. Unread banner component (shows when unread >= 5)
2. Summary card (dashed border, Brain icon, "Summary" label)
3. "Dismiss" + "Share to channel" buttons

### Order
1. Backend catchup endpoint
2. Unread banner + message count
3. Summary card display
4. Caching
5. Share to channel action
6. Inbox integration

---

## Priority Matrix

| Phase | Feature | Effort | Impact |
|-------|---------|--------|--------|
| **Done** | Pinned Messages | Small | Medium |
| **Next** | Inbox & Notifications (core) | Large | Critical |
| **Next** | Mobile Responsive (layout) | Medium | High |
| **Then** | Catch Me Up | Medium | High (differentiator) |
| **Then** | Search Improvements | Medium | Medium |
| **Later** | Browser Push | Medium | High |
| **Later** | Email Digest | Small | Medium |
| **Later** | PWA + Touch | Medium | Medium |
