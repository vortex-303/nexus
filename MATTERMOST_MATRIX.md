# Nexus vs Mattermost — Feature Matrix

## Legend
- **Have** = Nexus has this feature fully
- **Partial** = Nexus has a basic version
- **Gap** = Mattermost has it, Nexus doesn't
- **Nexus Only** = Nexus has it, Mattermost doesn't
- Priority: **P0** = critical, **P1** = high value, **P2** = nice to have, **P3** = low priority

---

## 1. MESSAGING

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Send/edit/delete messages | Yes | Yes | Have | — | |
| Emoji reactions | Yes | Yes | Have | — | |
| Typing indicators | Yes | Yes | Have | — | |
| Optimistic send + retry | No | Yes | Nexus Only | — | Just shipped |
| **Collapsed reply threads** | Yes | No | **Gap** | **P0** | Biggest missing chat feature. Without threads, channels become noisy fast. CRT with right-hand panel. |
| **Global threads view** | Yes | No | **Gap** | **P1** | "Threads" sidebar item showing all followed threads |
| **Pinned messages** | Yes | No | **Gap** | **P1** | Pin important messages per channel, viewable via header icon |
| **Saved/bookmarked messages** | Yes | No | **Gap** | **P2** | Personal message bookmarks across channels |
| **Message drafts** | Yes (synced) | No | **Gap** | **P1** | Drafts synced across devices, visible in sidebar |
| **Scheduled messages** | Yes | No | **Gap** | **P2** | Schedule future sends with time picker |
| **Message forwarding** | Yes | No | **Gap** | **P2** | Forward messages to other channels with comment |
| **Message priority** | Yes (Urgent/Important) | No | **Gap** | **P2** | Priority labels + persistent notifications for urgent |
| **Acknowledgement requests** | Yes | No | **Gap** | **P3** | "Acknowledge" button on messages |
| Quick emoji reactions (hover) | Yes | No | **Gap** | **P1** | Hover → recent emojis for one-click react |
| Custom emoji upload | Yes | No | **Gap** | **P2** | Workspace-custom emoji |
| GIF picker (Giphy/Tenor) | Yes | No | **Gap** | **P3** | |
| Voice messages | Plugin | No | **Gap** | **P3** | |
| Message permalink sharing | Yes | No | **Gap** | **P2** | Link to specific messages |
| AI-generated images in chat | No | Yes | Nexus Only | — | Gemini image gen via agents |
| Agent tool-use badges | No | Yes | Nexus Only | — | Shows which tools agent used |
| Agent thinking indicators | No | Yes | Nexus Only | — | Real-time agent state broadcast |

### Learn from Mattermost
- **Thread architecture**: Threads are root_id-based — every reply has a `root_id` pointing to the parent message. Simple, elegant. Channel timeline shows only root messages; replies expand in a panel.
- **Quick reactions UX**: Hovering a message shows 3-4 recently used emojis as small buttons. Reduces clicks from 3 to 1.
- **Draft sync**: Drafts stored server-side, appear as a sidebar category. Users never lose work.

---

## 2. CHANNELS

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Public channels | Yes | Yes | Have | — | |
| Private channels | Yes | Yes | Have | — | |
| Direct messages | Yes | Yes | Have | — | DM privacy fix just shipped |
| **Group DMs** | Yes (up to 7) | No | **Gap** | **P1** | Multi-person DMs without creating a channel |
| **Channel categories** | Yes (custom, drag-drop) | No | **Gap** | **P1** | User-created sidebar categories with drag-drop |
| **Channel favorites** | Yes | No | **Gap** | **P1** | Star channels to pin in Favorites section |
| **Channel muting** | Yes | No | **Gap** | **P1** | Suppress notifications per channel |
| Channel archiving | Yes (with unarchive) | Yes | Have | — | |
| **Channel header/purpose** | Yes (markdown) | No | **Gap** | **P2** | Editable header text + purpose description |
| **Channel bookmarks** | Yes (links/files pinned to header) | No | **Gap** | **P2** | Up to 50 links/files below header |
| Channel classification | No | Yes | Nexus Only | — | public/internal/confidential/restricted |
| **Unread filter mode** | Yes | No | **Gap** | **P1** | "Group unreads separately" in sidebar |
| Channel switcher (Cmd+K) | Yes | No | **Gap** | **P1** | Quick-jump search modal |
| **Convert public↔private** | Yes | No | **Gap** | **P3** | |
| Shared/federated channels | Yes (Enterprise) | No | **Gap** | **P3** | Cross-server channel sharing |
| DM sort (alpha vs recent) | Yes | No | **Gap** | **P2** | User preference for DM ordering |

### Learn from Mattermost
- **Sidebar categories**: Users create categories like "Projects", "Teams", drag channels in. Collapsible. Transforms a long channel list into organized workspace.
- **Channel switcher**: Cmd+K opens a fuzzy-search modal. Essential for workspaces with 20+ channels.

---

## 3. SEARCH

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Basic text search | Yes | Yes | Have | — | |
| **Search operators** (from:, in:, before:, after:) | Yes | No | **Gap** | **P1** | Essential for finding specific messages |
| **File search** | Yes (separate tab) | No | **Gap** | **P2** | Search uploaded files by name/type |
| Exact phrase ("quotes") | Yes | No | **Gap** | **P1** | |
| Exclude terms (-word) | Yes | No | **Gap** | **P2** | |
| Hashtags (#tag) | Yes | No | **Gap** | **P3** | Clickable hashtags |
| Recent mentions view | Yes | No | **Gap** | **P2** | Sidebar item showing all @mentions |
| AI-powered knowledge search | No | Yes | Nexus Only | — | Semantic search via embeddings |

---

## 4. NOTIFICATIONS

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| WebSocket real-time updates | Yes | Yes | Have | — | |
| Unread counts | Yes | Yes | Have | — | |
| Connection status banner | No | Yes | Nexus Only | — | Just shipped |
| **Desktop notifications** | Yes (native) | No | **Gap** | **P0** | Web Notification API — easy win |
| **Notification sound** | Yes | No | **Gap** | **P1** | Configurable alert sound |
| **Per-channel notification prefs** | Yes | No | **Gap** | **P2** | Override global settings per channel |
| **Do Not Disturb** | Yes (with expiry) | No | **Gap** | **P2** | Status that suppresses all notifications |
| **Custom notification keywords** | Yes | No | **Gap** | **P3** | Trigger on custom words |
| Mobile push notifications | Yes (HPNS) | No | **Gap** | **P3** | Requires mobile app |
| Email notifications | Yes (batched) | No | **Gap** | **P2** | Email digest for missed messages |

### Learn from Mattermost
- **Desktop notifications are trivial**: Just `Notification.requestPermission()` + `new Notification()`. Highest ROI gap to close.
- **Per-channel muting**: Simple boolean per channel. Big quality-of-life improvement.

---

## 5. FILE SHARING

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| File upload | Yes | Yes | Have | — | |
| Content-addressed storage | No | Yes | Nexus Only | — | SHA-256 dedup |
| File folders | No | Yes | Nexus Only | — | Hierarchical folder structure |
| File descriptions | No | Yes | Nexus Only | — | |
| **Drag & drop upload** | Yes | No | **Gap** | **P1** | Drop files onto message area |
| **Clipboard paste** (images) | Yes | No | **Gap** | **P1** | Ctrl+V to paste screenshot |
| **Image viewer** (lightbox) | Yes | Yes | Have | — | |
| **Inline video/audio playback** | Yes | No | **Gap** | **P2** | |
| Multiple attachments per message | Yes (5) | No | **Gap** | **P2** | |
| File size limits | Yes (configurable) | No | **Gap** | **P2** | |
| Public file links | Yes | No | **Gap** | **P3** | Shareable URLs for files |

---

## 6. INTEGRATIONS

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Incoming webhooks | Yes | Yes | Have | — | |
| Email integration (SMTP) | Plugin | Yes | Have | — | Built-in SMTP server |
| Telegram integration | Plugin | Yes | Have | — | |
| MCP server management | No | Yes | Nexus Only | — | Full MCP client with tool discovery |
| **Outgoing webhooks** | Yes | No | **Gap** | **P2** | Trigger external services on channel events |
| **Slash commands** (custom) | Yes | No | **Gap** | **P1** | User-defined slash commands hitting external APIs |
| **Bot accounts** | Yes | Partial | Partial | — | Agents are bot-like but not dedicated bot accounts |
| Plugin/extension system | Yes (Go+React) | No | **Gap** | **P3** | Server+client plugin architecture |
| **Interactive messages** (buttons, menus) | Yes | No | **Gap** | **P1** | Buttons and dropdowns in messages |
| REST API | Yes (OpenAPI) | Yes | Have | — | Full CRUD API |
| OAuth2 apps | Yes | No | **Gap** | **P3** | |

### Learn from Mattermost
- **Interactive messages**: Buttons and dropdown menus embedded in messages. Agents could use these for user confirmations, approvals, multi-choice responses instead of plain text.
- **Slash commands**: `/deploy staging`, `/standup`, `/poll` — simple HTTP callback pattern. Easy to implement.

---

## 7. USER MANAGEMENT

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Email/password auth | Yes | Yes | Have | — | |
| 9 specialized roles | No (6 generic) | Yes | Nexus Only | — | designer, marketing_*, researcher, sales |
| 30 granular permissions | Yes (similar) | Yes | Have | — | |
| Per-member permission overrides | No | Yes | Nexus Only | — | |
| Guest accounts | Yes | Yes | Have | — | |
| Org chart | No | Yes | Nexus Only | — | D3.js visualization |
| Member profiles (title, bio, goals) | Partial | Yes | Nexus Only | — | |
| **Custom status** (emoji + text) | Yes | No | **Gap** | **P1** | "In a meeting", "On vacation" with expiry |
| **Online/Away/DND/Offline status** | Yes (auto) | Partial | Partial | — | Have online/offline, no away/DND |
| **SSO (SAML, LDAP, OAuth)** | Yes | No | **Gap** | **P3** | Enterprise feature |
| **MFA/2FA** | Yes (TOTP) | No | **Gap** | **P2** | |
| Profile picture | Yes | No | **Gap** | **P2** | Avatar upload |
| **User groups** | Yes (LDAP sync) | No | **Gap** | **P3** | |

---

## 8. CONTENT & RICH MEDIA

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Markdown messages | Yes | Yes | Have | — | |
| Code blocks + syntax highlighting | Yes | Yes | Have | — | |
| Tiptap rich text editor (docs) | No | Yes | Nexus Only | — | |
| AI image generation | No | Yes | Nexus Only | — | |
| **Formatting toolbar** (messages) | Yes | No | **Gap** | **P2** | Bold/italic/code buttons above input |
| **Link previews** (Open Graph) | Yes | No | **Gap** | **P1** | URL cards with title, description, image |
| **@mention autocomplete** | Yes | No | **Gap** | **P0** | Type @ → dropdown of users/channels |
| LaTeX rendering | Yes | No | **Gap** | **P3** | |
| **Emoji picker** (in messages) | Yes | No | **Gap** | **P1** | Searchable emoji grid |

### Learn from Mattermost
- **@mention autocomplete is critical**: Users type `@` and get a filtered dropdown. Without this, mentioning agents/users is error-prone. Also enables `~channel` linking.

---

## 9. COLLABORATION

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Task management | Yes (Boards) | Yes | Have | — | Nexus has inline task boards |
| Documents/notes | No (plugin) | Yes | Nexus Only | — | Full Tiptap editor |
| Calendar | No (plugin) | Yes | Nexus Only | — | Events, recurrence, reminders |
| Knowledge base | No | Yes | Nexus Only | — | With semantic search |
| AI agents | Partial (Copilot) | Yes | Nexus Only | — | 8 built-in + custom agents + tools |
| AI memory extraction | No | Yes | Nexus Only | — | Auto-extract facts/decisions |
| **Voice/video calls** | Yes (WebRTC) | No | **Gap** | **P2** | Built-in voice, screen share |
| **Playbooks** (workflow automation) | Yes | No | **Gap** | **P2** | Incident response checklists |
| **Interactive workflows** | Yes | No | **Gap** | **P2** | Multi-step forms, approvals |

---

## 10. PERFORMANCE & ARCHITECTURE

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| WebSocket pub/sub | Yes | Yes | Have | — | |
| Prometheus metrics | Yes | Yes | Have | — | |
| Rate limiting | Yes | Yes | Have | — | Just shipped (200ms) |
| Backpressure (low-priority drops) | No | Yes | Nexus Only | — | Just shipped |
| Connection status + reconnect | No standard | Yes | Nexus Only | — | Exponential backoff, offline queue |
| Offline message queue | No | Yes | Nexus Only | — | Just shipped |
| Single binary deployment | No (multi-service) | Yes | Nexus Only | — | Embed everything |
| SQLite (zero-config DB) | No (PostgreSQL) | Yes | Nexus Only | — | |
| **Horizontal scaling** | Yes (cluster) | No | **Gap** | **P3** | Not needed until 1000+ users |
| **Read replicas** | Yes | No | **Gap** | **P3** | |
| **CDN/image proxy** | Yes | No | **Gap** | **P3** | |

---

## 11. DISTRIBUTION

| Feature | Mattermost | Nexus | Status | Priority | Notes |
|---------|-----------|-------|--------|----------|-------|
| Cloud SaaS | Yes | Yes | Have | — | Fly.io |
| Self-hosted binary | Yes | Yes | Have | — | Single binary |
| Docker | Yes | Yes | Have | — | |
| **Kubernetes/Helm** | Yes | No | **Gap** | **P3** | |
| **Desktop app** (Electron) | Yes | No | **Gap** | **P2** | Multi-server tabs, native notifications |
| **Mobile app** | Yes (React Native) | No | **Gap** | **P2** | iOS + Android |
| **PWA** | No | No | — | **P1** | Easy win — manifest.json + service worker |
| Air-gapped deployment | Yes | Yes | Have | — | No external deps required |

---

## TOP OPPORTUNITIES — RANKED BY IMPACT/EFFORT

### Quick Wins (< 1 day each)
1. **Desktop notifications** — `Notification API` in browser, ~50 lines of JS
2. **@mention autocomplete** — dropdown on `@` keystroke, filter members + agents
3. **Channel favorites** — boolean flag, sort starred channels to top
4. **Emoji picker** — open-source component, wire to reaction system
5. **Drag & drop file upload** — `ondragover`/`ondrop` on messages area
6. **Clipboard paste images** — `onpaste` handler for screenshots
7. **Channel switcher (Cmd+K)** — fuzzy search modal over channel list
8. **Quick emoji reactions** — hover → show 3 recent emojis

### Medium Effort (1-3 days each)
9. **Reply threads** — add `root_id` to messages, right-hand panel, thread following
10. **Channel categories** — user-created groups with drag-drop, collapsible
11. **Group DMs** — multi-person DMs (3-7 people)
12. **Channel muting** — per-channel notification suppression
13. **Message drafts** — server-synced per-channel drafts
14. **Link previews** — server-side Open Graph fetch, render card
15. **Pinned messages** — pin flag + pinned message list per channel
16. **Custom status** — emoji + text + expiry
17. **Search operators** — `from:`, `in:`, `before:`, `after:` parsing
18. **Interactive messages** — buttons/menus in agent responses
19. **Notification sound** — configurable audio alert

### Larger Efforts (3+ days)
20. **Slash commands** — custom commands hitting external HTTP endpoints
21. **Outgoing webhooks** — event-driven HTTP callbacks
22. **PWA** — manifest.json + service worker for installable web app
23. **Formatting toolbar** — bold/italic/code/link buttons in message input
24. **Voice calls** — WebRTC audio (or integrate Jitsi/LiveKit)
25. **Message scheduling** — schedule queue + cron job

---

## NEXUS UNIQUE ADVANTAGES (Things Mattermost Can't Match)

| Feature | Why It Matters |
|---------|---------------|
| **AI Agents with tools** | 8 built-in agents + custom, each with tool access, skills, knowledge. Mattermost's "Copilot" is a basic LLM wrapper. |
| **AI image generation** | Generate ad creatives, visuals directly in chat |
| **Memory extraction** | Auto-extract facts, decisions, commitments from conversations |
| **Knowledge base with semantic search** | Upload docs, search by meaning not just keywords |
| **MCP server integration** | Connect any MCP-compatible tool server |
| **Agent skills system** | Markdown playbooks that shape agent behavior |
| **Content-addressed file storage** | SHA-256 dedup, efficient storage |
| **Single binary deployment** | Zero external dependencies, runs on a Raspberry Pi |
| **Classification system** | Channel security classification levels |
| **Org chart** | Built-in organizational hierarchy visualization |
| **Calendar** | Native calendar with recurrence, reminders |
| **9 specialized roles** | Designer, marketing, researcher, sales — not just admin/member |
| **Per-member permission overrides** | Fine-grained beyond role-based |

---

## STRATEGIC TAKEAWAYS

1. **Threads are the #1 gap.** Every mature chat product has them. Without threads, Nexus channels become unusable at scale. Mattermost's `root_id` approach is clean and proven.

2. **Autocomplete (@mentions, Cmd+K) is the #2 gap.** These are muscle-memory UX patterns. Users expect them. Both are straightforward to implement.

3. **Desktop notifications are free.** The browser API exists. This is literally 50 lines of code for massive perceived improvement.

4. **Nexus's AI is a moat.** Mattermost's AI is bolted-on. Nexus has agents, tools, memory, knowledge, skills, and image generation deeply integrated. Double down here.

5. **Don't chase enterprise features.** SSO, LDAP, compliance exports — these matter for Fortune 500 sales. Focus on the collaboration UX that makes small/medium teams productive.

6. **Interactive messages unlock agent power.** Buttons and menus in messages would transform agent interactions from "chat with AI" to "AI-driven workflows."
