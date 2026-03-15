# Slack Integration Plan

## Overview

Add Slack as a corporate integration alongside Telegram. Brain responds to `@Brain` mentions in Slack channels, replies in-thread, and cross-posts conversations to a linked Nexus channel.

## Architecture

```
Slack channel → Slack Events API → POST /api/workspaces/:slug/brain/slack/events
    → Brain processes (same pipeline as chat)
    → Response posted back to Slack via Web API (in-thread)
    → Also posted to linked Nexus channel
```

## Registration

Slack App created at api.slack.com/apps:
- Bot scopes: `chat:write`, `app_mentions:read`, `channels:history`
- Event Subscription URL: `https://nexus-workspace.fly.dev/api/workspaces/{slug}/brain/slack/events`
- Slack sends a verification challenge on setup (endpoint returns the challenge string)

**Distribution modes:**
- **Single-workspace install** (for testing) — install to own Slack workspace, no review needed
- **Public distribution** (later) — requires Slack App Directory review (1-2 weeks, checks security/privacy/UX)

## Testing

1. Create Slack app at api.slack.com, install to test Slack workspace
2. Copy Bot Token → Brain Settings → Integrations → Slack
3. Invite bot to a Slack channel (`/invite @Brain`)
4. `@Brain what's on the roadmap?` → should get a threaded response
5. Check Nexus channel for cross-posted conversation
6. No ngrok needed — already on Fly

## Considerations

### Technical
- **Rate limits** — Slack allows ~1 msg/sec per channel. Send a "thinking..." reaction or typing indicator while Brain processes
- **Thread handling** — always reply in-thread to avoid spamming channels
- **Message formatting** — Slack uses mrkdwn (not standard markdown), need a converter
- **File/image handling** — if Brain generates images or users share files in Slack
- **Error handling** — if Brain fails, don't leave Slack users hanging (send error message)

### Product
- **Per-workspace** — each Nexus workspace gets its own Slack bot token (simpler)
- **Mentions-only** — only `@Brain` mentions trigger Brain (safer to start)
- **Admin-gated** — only Nexus admins can configure the Slack connection
- **Full context** — Brain in Slack has access to same memories/knowledge/skills as in Nexus (the selling point)

### Business/Legal
- Slack App Directory review requires privacy policy URL and support URL
- Disclose that user messages are sent through server to OpenRouter (privacy policy)
- Slack ToS: don't store messages longer than necessary
- Slack integration is a natural premium feature gate

## MVP Scope

- `@Brain` mentions only
- Reply in-thread
- Single Nexus channel as home for Slack conversations
- Bot token pasted manually in Brain Settings (no OAuth install flow yet)
- ~200 lines Go backend + UI section in integrations tab

### Backend (Go)
- `internal/server/slack.go` — event handler, verification challenge, message posting
- Slack Web API client (post message, add reaction)
- Register route: `POST /api/workspaces/:slug/brain/slack/events`
- Brain settings: `slack_bot_token`, `slack_channel_id` (linked Nexus channel)

### Frontend (Svelte)
- Brain Settings → Integrations tab: Slack section (bot token input, channel selector, autonomy dropdown)
- Same pattern as Telegram section

### Flow
1. Slack sends event to `/brain/slack/events`
2. Verify request (Slack signing secret)
3. Extract mention text, user info
4. Add 👀 reaction (acknowledge receipt)
5. Run through Brain pipeline (same as chat message)
6. Post response back to Slack thread via Web API
7. Cross-post to linked Nexus channel
