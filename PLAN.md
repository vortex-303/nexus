# Nexus — Implementation Plan

This document breaks the SPEC.md vision into concrete, ordered implementation steps. Each phase builds on the previous one and produces a usable product.

---

## Phase 1 — The Living Workspace [~15 weeks]

Goal: A complete team platform — chat, Brain, tasks, docs, contacts — usable from a browser. The Brain is there from second one.

### 1.1 — Server Skeleton
- [ ] Go project setup (modules, directory structure)
- [ ] CLI framework (`nexus serve`, `nexus version`, `nexus adduser`)
- [ ] HTTP server with auto-TLS (Let's Encrypt via `autocert`)
- [ ] Static file serving via `//go:embed` (placeholder HTML for now)
- [ ] SQLite setup: global `nexus.db` + per-workspace `workspace.db`
- [ ] Database migrations framework
- [ ] Config loading (CLI flags → config.toml → defaults)
- [ ] Health check endpoint

### 1.2 — Auth & Workspaces
- [ ] Instant workspace creation (random slug, no auth required)
- [ ] Anonymous sessions (display name, stored in browser)
- [ ] Optional account creation (email/password, bcrypt)
- [ ] JWT session tokens
- [ ] Workspace invite links (`/w/{id}?invite={token}`)
- [ ] Admin role assigned to workspace creator
- [ ] Member role assigned to invited users

### 1.3 — Real-Time Messaging
- [ ] WebSocket hub (goroutine per connection, in-process pub/sub)
- [ ] JSON message protocol (`{type, payload}`)
- [ ] Channels: create, list, join, leave, archive
- [ ] Channel types: public, private, DM
- [ ] Messages: send, edit, delete, reactions
- [ ] Typing indicators, presence (online/offline/busy)
- [ ] Message history with pagination
- [ ] Unread counts, last-read tracking
- [ ] Channel classification field (public/internal/confidential/restricted) — stored, not enforced yet

### 1.4 — Web UI (SvelteKit)
- [ ] SvelteKit project setup, build pipeline
- [ ] Embed built assets into Go binary
- [ ] Landing page: "Start a workspace" button
- [ ] Workspace view: sidebar (channels, DMs), message area, member list
- [ ] Channel creation, switching
- [ ] Real-time messaging (WebSocket client)
- [ ] Typing indicators, presence dots
- [ ] User settings (display name, avatar, role display)
- [ ] Workspace settings (admin only: name, invite links, member management)
- [ ] Responsive design (mobile browser usable)

### 1.5 — Roles & Permissions
- [ ] Role system: admin, member, designer, marketing_coordinator, marketing_strategist, researcher, sales, guest, custom
- [ ] Permission set (22 permissions — chat, tasks, contacts, brain tools, workspace)
- [ ] Role → default permissions mapping
- [ ] Admin can override individual permissions
- [ ] Admin can assign/change roles
- [ ] Permission checks on all API endpoints
- [ ] Guest restrictions (invited channels only, no Brain DM, no contacts)

### 1.6 — Tasks
- [ ] Task CRUD API
- [ ] Create tasks from chat (Brain or manual)
- [ ] Assign to humans or Brain
- [ ] Status: backlog → todo → in_progress → done → cancelled
- [ ] Priority: low, medium, high, urgent
- [ ] Due dates, tags
- [ ] Link tasks to messages, contacts, decisions
- [ ] Board view in UI (kanban columns by status)
- [ ] List view in UI (sortable, filterable)
- [ ] Task notifications (assigned, overdue, completed)

### 1.7 — File Sharing
- [ ] File upload API (multipart)
- [ ] Content-addressed storage (SHA-256, blob directory)
- [ ] Inline preview in messages (images, PDFs)
- [ ] File list per channel / per workspace
- [ ] Download links
- [ ] Size limits (configurable, default 50MB)
- [ ] Role-gated: designer gets image generation tools alongside upload

### 1.8 — Documents & Notes (Tiptap Block Editor)
- [ ] Tiptap integration via svelte-tiptap (ProseMirror foundation)
- [ ] Custom block UI components in Svelte (slash menu, block toolbar, drag-and-drop)
- [ ] Block types: paragraph, heading, list, checklist, code, image, table, callout, divider
- [ ] Inline mentions: @person, #channel, [[document]], task links, contact links
- [ ] Yjs CRDT collaboration (real-time multi-user editing via WebSocket)
- [ ] Cursor and selection presence (see who's editing where)
- [ ] Yjs sync server in Go (WebSocket provider)
- [ ] Document storage in workspace SQLite (Tiptap JSON format)
- [ ] Document CRUD API (create, read, update, delete, list, search)
- [ ] Document sharing: workspace-wide, channel-specific, or private
- [ ] Document templates: meeting notes, project brief, client proposal, weekly report
- [ ] Link documents to tasks, contacts, channels, messages
- [ ] Document list view in UI (sortable, filterable, searchable)
- [ ] Brain reads documents as part of knowledge graph (index Tiptap JSON)
- [ ] Brain creates and updates documents (via JSON API)
- [ ] Brain generates documents from templates with context

### 1.9 — Brain Engine (Core)
- [ ] Brain identity auto-created per workspace
- [ ] Brain definition files: SOUL.md, INSTRUCTIONS.md, TEAM.md, MEMORY.md, HEARTBEAT.md
- [ ] System prompt assembly from definition files (with char limits)
- [ ] OpenRouter integration (HTTP client, streaming responses)
- [ ] Default text model + default image model configurable per workspace
- [ ] Brain responds to @mentions in channels
- [ ] Brain responds to DMs
- [ ] Brain has its own WebSocket presence (always online)
- [ ] Attention system: high (@mentions), medium (public channels), low (DMs only if invited)
- [ ] Configurable attention level per workspace
- [ ] Role-aware responses (Brain adapts based on who's asking and their role)

### 1.10 — Brain Memory System
- [ ] Layer 1: Raw messages stored in SQLite (already done by 1.3)
- [ ] Layer 2: Working memory — per-channel rolling summaries, updated every N messages
- [ ] Layer 3: Extracted facts — atomic memory extraction (decisions, commitments, people, projects)
- [ ] Layer 4: Knowledge graph — relationships between entities, stored as SQLite rows
- [ ] Layer 5: Semantic index — sqlite-vec embeddings for document search (optional, can defer)
- [ ] Query resolution: working memory → entities → graph → hybrid search
- [ ] Auto-flush: detect token limit, save context, compact session
- [ ] Brain-maintained TEAM.md (auto-updates as it learns about members)
- [ ] Brain-maintained MEMORY.md (long-term curated knowledge)

### 1.11 — Heartbeat Scheduler
- [ ] Parse HEARTBEAT.md for schedule definitions
- [ ] Cron goroutine runner (daily, weekly, hourly, on-idle triggers)
- [ ] Morning brief: overdue tasks, approaching deadlines
- [ ] Weekly summary: decisions, completed tasks, open items
- [ ] Hourly: compress working memory for active channels
- [ ] Idle: run memory consolidation when no messages for 2+ hours
- [ ] Each heartbeat runs as main-context or isolated session (configurable)

### 1.12 — Core Skills (Bundled)
- [ ] Skill loading: read SKILL.md files from brain/skills/ directory
- [ ] YAML frontmatter parsing (name, description, schedule, channels, autonomy, roles, tools)
- [ ] Bundled skill: **Daily Standup** — async standup, collect responses, compile summary
- [ ] Bundled skill: **Meeting Notes** — detect multi-person conversations, offer to capture
- [ ] Bundled skill: **Decision Logger** — detect consensus, confirm, create Decision artifact
- [ ] Bundled skill: **New Hire Buddy** — welcome new members, DM overview, check-ins

### 1.13 — Contacts & CRM
- [ ] Contact CRUD API
- [ ] Contact types: client, lead, vendor, partner, candidate, other
- [ ] Contact owner (team member who manages relationship)
- [ ] Deal pipeline: lead → qualified → proposal → negotiation → closed_won/lost
- [ ] Deal value, expected close date
- [ ] Link contacts to channels, tasks
- [ ] Auto-track last interaction date
- [ ] Contact list view in UI
- [ ] Pipeline board view in UI (kanban by deal stage)
- [ ] Contact card sidebar
- [ ] Role-gated: only sales + admin can manage contacts (others can view)

### 1.14 — Observability
- [ ] Brain action log (every action: what, why, confidence, outcome)
- [ ] Action log viewable in UI (admin + member)
- [ ] Self-critique logging (when Brain reviews its own output)
- [ ] Token usage tracking per Brain action
- [ ] Cost estimate per action (based on model pricing)

---

## Phase 1 — Build Order

Phase 1 is large. Here's the suggested build order (each step produces something testable):

```
Week 1-2:   1.1 Server Skeleton + 1.2 Auth & Workspaces
            → Result: nexus serve runs, creates workspaces, serves placeholder UI

Week 3-4:   1.3 Real-Time Messaging + 1.4 Web UI (basic)
            → Result: people can chat in real-time in a browser

Week 5:     1.5 Roles & Permissions
            → Result: admin assigns roles, permissions enforced

Week 6:     1.6 Tasks
            → Result: create tasks, assign, board view

Week 7:     1.7 File Sharing
            → Result: upload and share files in channels

Week 8:     1.8 Documents & Notes (Tiptap)
            → Result: block editor, real-time collab, document library

Week 9-10:  1.9 Brain Engine (Core)
            → Result: Brain responds to @mentions, role-aware, OpenRouter

Week 11:    1.10 Brain Memory System
            → Result: Brain remembers conversations, reads docs, extracts facts

Week 12:    1.11 Heartbeat + 1.12 Core Skills
            → Result: morning briefs, standup bot, decision logging

Week 13:    1.13 Contacts & CRM
            → Result: contact management, deal pipeline

Week 14-15: 1.14 Observability + polish
            → Result: Brain action log, cost tracking, bug fixes
```

**Milestone: End of Week 4** — usable chat platform (no AI yet, but functional)
**Milestone: End of Week 8** — chat + tasks + docs + files (full collaboration, no AI yet)
**Milestone: End of Week 10** — Brain is live and responding
**Milestone: End of Week 15** — full Phase 1, ready for beta users

---

## Phase 2 — The Brain Reaches Out [~8 weeks]

Goal: The Brain communicates beyond the web UI — email, Telegram, webhooks. It becomes the team's communication hub.

### 2.1 — Email Integration
- [ ] SMTP inbound server (receive emails to brain-{workspace}@nexus.app)
- [ ] Email parsing (sender, recipients, subject, body, attachments)
- [ ] Brain processes inbound email: classify, decide action, route
- [ ] SMTP outbound (send emails from Brain)
- [ ] Autonomy controls: autonomous / draft+approve / never
- [ ] Auto-create contacts from email senders
- [ ] Email threads linked to contacts and channels
- [ ] Email restriction settings: only known contacts / only internal / anyone

### 2.2 — Social Scheduling
- [ ] .ics calendar file generation for meeting scheduling
- [ ] Calendar deep links (Google Calendar, Outlook, Apple Calendar)
- [ ] Brain coordinates via chat + email (asks availability, proposes times)

### 2.3 — Telegram Bridge
- [ ] Telegram Bot API integration (long polling)
- [ ] Session mapping: Telegram thread → Nexus channel/context
- [ ] Same Brain identity, same memory, same personality
- [ ] Telegram-specific formatting (Markdown → Telegram HTML)

### 2.4 — Webhooks
- [ ] Webhook endpoint per workspace (`/w/{id}/hook/{secret}`)
- [ ] Receive JSON payloads from any app
- [ ] Brain interprets webhook events via LLM
- [ ] Route to relevant channel, create tasks/contacts as needed
- [ ] Webhook management UI (regenerate secret, view recent events)

### 2.5 — Channel Adapter Framework
- [ ] Adapter interface: normalize → route → format
- [ ] Foundation for future bridges (WhatsApp, Slack, etc.)
- [ ] Unified identity: same Brain across all surfaces

---

## Phase 3 — Deep Intelligence [~10 weeks]

Goal: Full skills system, sub-agents, think-while-idle, document ingestion. The Brain becomes proactive.

### 3.1 — Full Skills System
- [ ] Natural language skill creation ("@Brain create an agent that...")
- [ ] Brain generates SKILL.md from description
- [ ] Skill marketplace UI (browse, install, customize)
- [ ] Role-gated skill triggers (only sales can trigger deal skills)
- [ ] Skill enable/disable per workspace

### 3.2 — Advanced Skills (Bundled)
- [ ] Client Onboarding — channel creation, checklist, welcome email, CRM entry
- [ ] Proposal Tracker — follow-up cadence, deal stage updates
- [ ] Support Triage — categorize, assign, respond, escalate
- [ ] Campaign Manager — channel, task checklist, asset tracking, daily briefs
- [ ] Content Calendar — schedule tracking, nudges, weekly summary
- [ ] Competitive Intel — weekly web search, digest in #strategy
- [ ] Invoice Reminder — deadline tracking, escalation
- [ ] Hiring Pipeline — candidate tracking, interview feedback, weekly summary
- [ ] Expense Tracker — receipt parsing, monthly summaries
- [ ] Release Notes — compile from completed tasks, draft for review

### 3.3 — Sub-Agent Runtime
- [ ] Sub-agent goroutines with isolated sessions
- [ ] Bounded communication (max 5 rounds, skip tokens)
- [ ] Provenance tracking (human / brain / sub-agent)
- [ ] Allowlisted tools per sub-agent (from SKILL.md)
- [ ] Self-critique pass before autonomous actions (extra LLM call)

### 3.4 — Think While Idle
- [ ] Overnight consolidation pass
- [ ] Morning synthesis generation (before anyone logs in)
- [ ] Idle-time entity merging and relationship updates
- [ ] Gap identification ("discussed but never decided")
- [ ] Meeting preparation (pre-load context before scheduled meetings)

### 3.5 — Document Ingestion
- [ ] Upload documents to workspace (PDF, DOCX, XLSX, CSV, TXT)
- [ ] Text extraction and chunking
- [ ] Embedding generation (OpenRouter or local model)
- [ ] sqlite-vec index for semantic search
- [ ] Brain can answer questions citing documents
- [ ] Staleness detection ("this doc hasn't been updated in 6 months")

### 3.6 — MCP Client
- [ ] MCP protocol client in Go
- [ ] MCP server management (start, stop, health check)
- [ ] Tool discovery (auto-detect available tools from MCP server)
- [ ] Google Calendar MCP integration
- [ ] Google Drive MCP integration
- [ ] MCP settings UI (add server, view tools, status)

### 3.7 — More Channel Bridges
- [ ] WhatsApp bridge (Baileys or official API)
- [ ] Slack bridge (Socket Mode, Bolt framework) — migration path from Slack to Nexus
- [ ] Session mapping (platform thread → Nexus channel/context)

### 3.8 — Cost Dashboard
- [ ] Token usage per Brain action, per channel, per user, per skill
- [ ] Cost estimation based on model pricing
- [ ] Monthly spend summary
- [ ] Budget alerts (warn at 80%, pause at 100%)
- [ ] Cost comparison: OpenRouter vs local llama.cpp savings

### 3.9 — Custom Roles
- [ ] Admin creates custom roles with selected permissions
- [ ] Role templates (copy existing role, modify)
- [ ] Role management UI

---

## Phase 4 — Specialization [~8 weeks]

Goal: Professional roles with custom models, AI agent team members, industry-specific Brain adaptations.

### 4.1 — Professional Roles
- [ ] Role profiles as Markdown (`brain/roles/lawyer.md`) extending Brain behavior
- [ ] Professional roles: lawyer, creative director, accountant, HR manager, project manager, developer, content writer, customer success, executive
- [ ] Per-role model routing via OpenRouter (legal → legal model, design → image model)
- [ ] Role-specific auto-skills (installing role auto-installs relevant skills)

### 4.2 — AI Agent Roles
- [ ] Deploy autonomous agents as workspace members
- [ ] Agent types: Legal Reviewer, Brand Guardian, Data Analyst, Compliance Officer, Meeting Facilitator, Translator
- [ ] Agent identity: names, avatars, presence, their own token budget
- [ ] Agent-to-agent coordination (via Brain as orchestrator)

### 4.3 — llama.cpp Integration
- [ ] llama.cpp sidecar: `nexus serve --llama-model ./model.gguf`
- [ ] Spawn and manage llama-server process
- [ ] Route confidential/restricted channels to local model
- [ ] Fallback: local → OpenRouter (or OpenRouter → local, configurable)
- [ ] Model management UI (loaded model, VRAM usage, status)

### 4.4 — Model Management
- [ ] Configure which models are available per workspace
- [ ] Set default models per role
- [ ] Model routing rules: "legal queries NEVER go to external models"
- [ ] Confidential channel classification enforced — local LLM only

---

## Phase 5 — Fortress [~8 weeks]

Goal: E2E encryption, SSO, compliance. Deployable in regulated industries.

### 5.1 — E2E Encryption
- [ ] MLS-based encryption (RFC 9420)
- [ ] Brain gets MLS group membership (users control which channels it reads)
- [ ] Message classification enforcement (confidential = local LLM only, restricted = ephemeral + local only)
- [ ] Ephemeral channels (auto-delete with cryptographic proof of deletion)
- [ ] BYOK (Bring Your Own Key) — workspace manages its own encryption keys

### 5.2 — Authentication & Compliance
- [ ] OIDC/SAML SSO (Okta, Google Workspace, Azure AD)
- [ ] Two-factor authentication
- [ ] IP allowlisting
- [ ] Compliance export (designated officer can decrypt for legal holds)
- [ ] Audit logging (all admin actions, Brain actions, permission changes, data access)
- [ ] Security audit command: `nexus security audit`

---

## Phase 6 — Everywhere [~12 weeks]

Goal: Native clients, offline support, ecosystem expansion.

### 6.1 — Native Clients
- [ ] iOS app (Swift, WebSocket protocol, push notifications via APNs)
- [ ] Mac app (optional native wrapper, menu bar presence)
- [ ] Device calendar integration (EventKit read/write)

### 6.2 — Offline & Sync
- [ ] CRDT sync for docs (Yjs), messages, and tasks
- [ ] Offline-first — everything syncs when reconnected
- [ ] Brain catches up and processes what happened while offline

### 6.3 — Local LLM on Device
- [ ] llama.cpp in native apps (Apple Silicon)
- [ ] Queries never leave the device

### 6.4 — Ecosystem
- [ ] OAuth integrations (Google Workspace, Microsoft 365)
- [ ] Skill marketplace (community-contributed skills, one-click install)
- [ ] Developer API (external apps interact with Nexus workspaces)
- [ ] Multi-server federation
- [ ] White-label / custom branding

---

## What's NOT in Phase 1

Explicitly deferred to keep scope manageable:

- Email integration (Phase 2)
- Telegram bridge (Phase 2)
- Webhooks (Phase 2)
- Sub-agents / advanced skills (Phase 3)
- MCP integrations (Phase 3)
- WhatsApp/Slack bridges (Phase 3)
- Document ingestion / RAG (Phase 3)
- Professional roles (Phase 4)
- AI agent roles (Phase 4)
- llama.cpp / local LLM (Phase 4)
- E2E encryption (Phase 5)
- SSO (Phase 5)
- iOS / Mac apps (Phase 6)
- Skill marketplace (Phase 6)
