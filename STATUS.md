# Nexus — Status & Next Steps

> Last updated: 2026-03-30

## Current State

Nexus is a fully functional AI-native team workspace deployed at **nexusteams.dev** and **nexus-workspace.fly.dev**. Single Go binary (~15MB) with embedded SvelteKit frontend.

### What's Live

| Area | Features |
|------|----------|
| **Chat** | Channels (public/private/DM), markdown, file upload (50MB), typing indicators, @mentions, /commands, thread replies with Brain auto-follow |
| **Brain AI** | @Brain mentions, DM with Brain, 2-round tool calling, system prompt from definition files + memories + skills + knowledge |
| **Memory** | Auto-extraction (decisions/commitments/people), rule-based patterns (no API cost), pin-to-memory (decision/commitment/policy/person) |
| **Notifications** | Bell icon + dropdown inbox (All/Mentions/DMs tabs), desktop notifications, welcome notification on join. Triggers: @mention, DM, task assignment |
| **Unread** | Badges in sidebar, browser tab title count `(3) Nexus`, unread channels sort to top, cross-tab sync |
| **Pagination** | Infinite scroll with IntersectionObserver, cursor-based backend (`?before=`) |
| **Pinned messages** | Pin/unpin via hover, thumbtack icon in header toolbar with count, pins panel, click-to-scroll |
| **Tasks** | Full CRUD, 5 statuses, 4 priorities, tags, assignee, due dates, board/list views, task scheduler (recurring + auto-assign), drag-drop |
| **Documents** | Tiptap rich editor (headings/lists/code/images/checklists), auto-save, real-time sync |
| **Agents** | Full CRUD, 9 templates, AI config generation, per-agent skills, delegation, built-in agents (Creative Director, Caly) |
| **MCP tools** | stdio + SSE transports, 15+ template catalog, per-workspace, auto-reconnect |
| **Knowledge** | Text/file/URL import, semantic vector search (Qdrant) with SQL LIKE fallback |
| **Skills** | Markdown + YAML frontmatter, enable/disable, AI generation, templates |
| **Org chart** | D3 hierarchy, drag-drop reparenting, role slots, member profiles |
| **Integrations** | Webhooks (token-auth), inbound SMTP email, Telegram bot |
| **Auth** | Email/password accounts, login page, invite links/codes, JWT HS256, 9 roles, 31 permissions |
| **Superadmin** | `SUPERADMIN_EMAIL` env var (default: nruggieri@gmail.com), first-user-is-admin fallback, `nexus admin promote <email>` CLI |
| **Platform admin** | Stats, workspace management, model curation, audit log, announcements, impersonation |
| **Heartbeat** | Cron-like scheduler for Brain actions (daily standup, weekly reports) |
| **License** | Ed25519 signed key enforcement |
| **Landing page** | Standalone HTML at `/landing.html` — accurate claims (15MB binary, Ollama, 200+ models) |
| **Chat header** | Horizontal toolbar: online count, pinned toggle, members toggle with pill buttons |

### Infrastructure

| Component | Value |
|-----------|-------|
| Fly.io app | `nexus-workspace`, `ewr` region |
| Machine | shared-cpu-1x, 512MB RAM |
| Domains | `nexusteams.dev`, `nexus-workspace.fly.dev` |
| Volume | `nexus_data` at `/data` |
| Deploy | `fly deploy` from `/Users/n/nexus/` |
| Local dev | `make dev` (builds web + Go, port 3000) |
| Optional | Redis (Upstash) for task queue, Qdrant for vector search |

---

## Today's Session (2026-03-30)

### Commits

1. `d3abe70` — Pin-to-memory, task scheduler, toast notifications, login page, channel & task improvements
2. `b8dca8d` — Replace hardcoded superadmin seed with env var + first-user-is-admin + CLI promote
3. `71c6a28` — Unread badges, infinite scroll, inbox & notifications, landing page fixes
4. `20b332b` — Fix notification panel visibility (sidebar overflow clipping)
5. `7a2de37` — Redesign chat header toolbar, welcome notifications on join

### What Was Built

- **P0:** Landing page accuracy (5→15MB, llama.cpp→Ollama)
- **P0:** Superadmin no longer hardcoded in source — env var + CLI promote
- **P1:** Unread badges — title count, channel sorting, cross-tab sync
- **P1:** Infinite scroll — IntersectionObserver replaces manual button
- **New:** Inbox & notifications — full system with DB, triggers, bell icon, dropdown, desktop notifications
- **New:** Chat header toolbar redesign — horizontal pills with active states
- **New:** Welcome notifications on workspace creation and member join

---

## Next Steps

### P0: Ship-Blockers (remaining)

- [ ] **Cut first GitHub release** — `git tag v0.1.0 && git push origin v0.1.0` → goreleaser creates binaries + Docker images
- [ ] **README.md** — what it is, screenshot, install instructions
- [ ] **Remove hardcoded superadmin** — ✅ Done (env var + CLI)

### P1: Core UX Gaps

- [ ] **Emoji reaction picker** — backend works, `sendReaction`/`removeReaction` exist, just need picker UI
- [ ] **Message edit button** — backend handles `message.edit`, need hover action + edit UI
- [ ] **Streaming Brain responses** — `CompleteStream` exists in openrouter.go, need SSE handler for real-time typing
- [ ] **Task detail modal** — description editor, assignee picker, due date picker, comments

### P2: Notification System Enhancements

- [ ] **Notification preferences** — per-channel mute levels (all/mentions/nothing), table exists but no UI
- [ ] **Browser Web Push** — VAPID keys + Service Worker for background notifications
- [ ] **Email digest** — daily summary via SendGrid (optional)
- [ ] **Thread reply notifications** — notify thread participants on new replies

### P3: Product Maturity

- [ ] **Mobile responsive** — bottom nav, collapsible sidebar, touch interactions, PWA
- [ ] **"Catch Me Up"** — AI summarize missed messages (differentiator feature)
- [ ] **Search improvements** — channel-scoped, person filter, date range, `/search` command
- [ ] **Collaborative editing** — Tiptap + Y.js/CRDT for multi-cursor docs
- [ ] **Thread support UI** — inline thread view (backend exists)

### P4: Scale & Distribution

- [ ] Upgrade Fly machine (2 CPU, 2GB RAM)
- [ ] S3/R2 blob storage
- [ ] CDN for static assets
- [ ] Rate limiting on public endpoints
- [ ] Stripe billing integration
- [ ] SSO/SAML (Business tier gate)

---

## Key Commands

```bash
make dev              # Local development (port 3000)
make build            # Build binary
fly deploy            # Deploy to production
nexus admin promote   # Promote account to superadmin
nexus db global       # Query global SQLite DB
nexus db <slug>       # Query workspace SQLite DB
```
