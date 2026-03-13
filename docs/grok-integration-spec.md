# Grok (xAI) Integration Spec — Nexus

## Executive Summary

Grok is xAI's AI platform with a unique moat: **first-party access to real-time X/Twitter data**. No other LLM provider has this. Combined with OpenAI-compatible APIs, competitive pricing, 2M token context, image/video generation, and built-in web search — Grok is a high-value addition to Nexus as both an LLM provider and a data source for social intelligence features.

---

## 1. Product Opportunity

### What Grok Gives Nexus That Nothing Else Can

| Capability | Current State in Nexus | With Grok |
|---|---|---|
| Real-time X/Twitter data | None | Brand monitoring, trend alerts, competitor tracking, sentiment analysis |
| Web search in Brain | Via MCP tools only | Built-in server-side web search ($0.005/call) |
| LLM provider options | OpenRouter (multi-provider) | Direct xAI API — cheaper, 2M context, faster |
| Image generation | None (removed Gemini image) | Aurora — $0.02/image (cheap, high quality, text rendering) |
| Video generation | None | Grok Imagine — $0.05/second, 6s clips from text |
| Code execution | None | Server-side Python sandbox for data analysis |

### Why This Matters for Teams Using Nexus

1. **Marketing teams** — Track brand mentions, campaign sentiment, competitor activity on X in real-time, directly in their workspace
2. **Sales teams** — Monitor prospect companies and key people on X, get alerts on relevant announcements
3. **Research teams** — Combine web + X search for comprehensive research on any topic
4. **Any team** — A faster, cheaper Brain with real-time awareness of current events

---

## 2. Grok API Overview

### Models

| Model | Price (in/out per 1M) | Context | Key Strengths |
|---|---|---|---|
| `grok-4-1-fast` | $0.20 / $0.50 | 2M | **Best for production** — fast, cheap, tool-calling, reasoning |
| `grok-4.20-beta` | $2.00 / $6.00 | 2M | Flagship reasoning, multi-agent capable |
| `grok-code-fast-1` | $0.20 / $1.50 | 256K | Code-optimized |
| `grok-imagine-image` | $0.02/image | — | Image generation (Aurora) |
| `grok-imagine-image-pro` | $0.07/image | — | Higher quality image gen |
| `grok-imagine-video` | $0.05/sec | — | 6s video clips from text |

### API Compatibility

- **Base URL:** `https://api.x.ai/v1`
- **Format:** OpenAI-compatible (Chat Completions API)
- **Tool calling:** Full support — same format Nexus already uses
- **Streaming:** SSE, same as OpenRouter
- **Authentication:** `Authorization: Bearer <API_KEY>`
- **OpenRouter:** Available as `x-ai/grok-4-1-fast`, `x-ai/grok-4`, etc.
- **Free credits:** $25 on signup + $150/mo via data sharing program

### Two API Surfaces

1. **Chat Completions API** (`/v1/chat/completions`) — OpenAI-compatible, works through OpenRouter, supports custom tool calling. This is what Nexus uses today.

2. **Responses API** (`/v1/responses`) — xAI-specific, required for built-in server-side tools (`web_search`, `x_search`, `code_execution`). Different request format. **This is where the unique value lives.**

---

## 3. Integration Phases

### Phase 1: Model Availability (Zero Code Changes)

**Effort:** None — already works
**Value:** Low-cost alternative LLM

Grok models are already available on OpenRouter. Users with an OpenRouter API key can select Grok models today. We just need to surface them in the UI.

**Changes:**
- Add `x-ai/grok-4-1-fast` and `x-ai/grok-4` to the recommended models list in Brain settings
- Add Grok to the agent model selector (`KNOWN_AGENT_MODELS`)

**Outcome:** Users get access to a $0.20/1M-token model with 2M context and full tool calling.

---

### Phase 2: Direct xAI Provider (Low Effort)

**Effort:** ~1 day
**Value:** Cheaper than OpenRouter, cached prompt discounts, foundation for Phase 3

Add xAI as a direct API provider alongside OpenRouter in the Go backend. Since the Chat Completions format is identical, this is just an alternative base URL + API key.

**Backend changes:**

`internal/brain/openrouter.go`:
- Add `xaiURL = "https://api.x.ai/v1/chat/completions"` constant
- Add `XAIKey` field to workspace brain settings
- Route to xAI when model starts with `grok-` and xAI key is configured
- Everything else (request format, response parsing, tool calling, streaming) stays identical

`internal/server/brain_settings.go`:
- Add `xai_api_key` to brain settings schema

**Frontend changes:**

`web/src/routes/(app)/w/[slug]/+page.svelte` (Brain settings panel):
- Add xAI API key input field
- Show Grok-specific models when xAI key is configured

**Database:**
- Add `xai_api_key` column to workspace brain settings (migration)

---

### Phase 3: X/Twitter Intelligence (Medium Effort, Highest Unique Value)

**Effort:** ~3-5 days
**Value:** Unique feature no competitor offers — real-time social intelligence in workspace

This is the **key differentiator**. Integrate xAI's Responses API to access `x_search` and `web_search` built-in tools.

#### 3a. Responses API Client

New file: `internal/brain/xai_responses.go`

```
XAIResponsesClient:
  - CompleteWithBuiltinTools(systemPrompt, messages, tools) -> response
  - Supports: web_search, x_search, code_execution
  - Different request format from Chat Completions
  - Returns citations and search results alongside text
```

Cost: $0.005 per tool invocation (web_search or x_search).

#### 3b. Brain Integration

Extend the Brain's tool execution to optionally use Grok's built-in tools:

- When Brain model is Grok + user asks about current events, trends, or social data → use `web_search` or `x_search` server-side
- Return citations in Brain responses
- Falls back to existing MCP web search tool if xAI not configured

#### 3c. Social Pulse Feature (New)

New workspace feature: **Social Pulse** — a channel-like view that shows real-time X/Twitter intelligence.

**Product concept:**
- Workspace admins configure **tracking topics** (keywords, hashtags, X handles, companies)
- System periodically queries `x_search` API (configurable interval: 1h, 4h, daily)
- Results are analyzed by Grok for relevance and sentiment
- Summaries are posted to a dedicated "Social Pulse" channel or shown in a dashboard
- Team members can click through to original X posts

**Implementation:**
- `internal/social/pulse.go` — Background worker that runs X searches on schedule
- `internal/server/social_settings.go` — CRUD for tracking topics per workspace
- `internal/db/migrations/` — New tables: `social_topics`, `social_alerts`
- Frontend: New "Social" page (like Calendar, Tasks) or a dedicated channel type

**Example queries the system would run:**
- `"@competitor_brand" sentiment:negative` — Track competitor complaints
- `"#industry_hashtag" viral` — Catch trending industry content
- `"from:key_person"` — Monitor thought leaders
- `"product_name" -from:our_handle` — What others say about our product

**Cost estimation:**
- 10 tracked topics × 4 searches/day × $0.005/search = $0.20/day/workspace
- Very affordable for high-value intelligence

#### 3d. Brain Tool: `search_x`

Add a new Brain tool that agents and Brain can use:

```go
{
    Name: "search_x",
    Description: "Search X/Twitter for real-time posts, mentions, and trends",
    Parameters: {
        "query": "Search query (keywords, hashtags, @handles)",
        "mode": "keyword|semantic|handle",
        "recency": "24h|48h|7d"
    }
}
```

This makes X search available to:
- Brain assistant (in DM conversations)
- Custom AI agents (e.g., a "Social Media Monitor" agent)
- Users via slash commands (`/search-x competitor_name`)

---

### Phase 4: Image Generation (Low Effort, Nice-to-Have)

**Effort:** ~1 day
**Value:** Cheap image generation in chat and documents

Integrate Grok Imagine (Aurora) for image generation:

- Add `/v1/images/generations` endpoint support
- Brain tool: `generate_image` using Aurora ($0.02/image)
- Inline image previews in chat messages
- Save generated images to workspace files (content-addressed storage)

Cheaper than any alternative ($0.02 vs $0.04+ for DALL-E).

---

## 4. Architecture Impact

### What Changes

| Component | Change | Scope |
|---|---|---|
| `internal/brain/openrouter.go` | Add xAI direct provider routing | Small — same format |
| `internal/brain/xai_responses.go` | New — Responses API client | New file, ~200 lines |
| `internal/brain/brain_tools.go` | Add `search_x` tool | ~30 lines |
| `internal/social/pulse.go` | New — Social Pulse background worker | New file, ~300 lines |
| `internal/server/server.go` | Register social pulse routes | ~5 lines |
| `internal/db/migrations/` | Social topics + alerts tables, xai_api_key | New migration |
| `web/src/routes/(app)/w/[slug]/+page.svelte` | xAI API key in Brain settings | ~20 lines |
| `web/src/routes/(app)/w/[slug]/social/` | New Social Pulse page | New route |

### What Stays The Same

- WebSocket protocol — unchanged
- Existing Brain tool calling loop — unchanged (Grok uses same format)
- OpenRouter integration — unchanged (Grok available there too)
- WebLLM local inference — unchanged (orthogonal)
- MCP integration — unchanged
- All other features — unchanged

### Data Flow: Brain with Grok

```
User message → Brain engine
  → If Grok model + xAI key:
      → Chat Completions API (tool calling, same as OpenRouter)
      → OR Responses API (built-in web_search/x_search)
  → Else: OpenRouter (existing path)
  → Tool execution → Results → Final response
```

### Data Flow: Social Pulse

```
Cron trigger (configurable interval)
  → For each tracked topic in workspace:
      → xAI Responses API with x_search tool
      → Grok analyzes results (sentiment, relevance, summary)
      → Store alert in DB
      → Post summary to Social Pulse channel via WebSocket
```

---

## 5. Cost Analysis

### Per-Workspace Monthly Estimates

| Usage Pattern | OpenRouter (current) | Grok Direct (Phase 2) | Grok + Social Pulse (Phase 3) |
|---|---|---|---|
| Brain: 100 queries/day, ~1K tokens each | ~$15-30/mo (varies by model) | ~$3/mo (grok-4-1-fast) | ~$3/mo |
| Social Pulse: 10 topics, 4x/day | N/A | N/A | ~$6/mo ($0.005 × 40 × 30) |
| Image generation: 50 images/mo | N/A | N/A | ~$1/mo |
| **Total** | **~$15-30/mo** | **~$3/mo** | **~$10/mo** |

Grok direct is **5-10x cheaper** than typical OpenRouter usage for equivalent quality.

### Free Tier Opportunity

xAI offers $25 signup credit + $150/mo via data sharing = $175/mo free. For small teams, Grok Brain usage could be **entirely free**.

---

## 6. Competitive Positioning

### What This Gives Nexus Over Competitors

| Feature | Slack | Teams | Notion | Discord | **Nexus + Grok** |
|---|---|---|---|---|---|
| AI assistant | Slack AI ($10/user) | Copilot ($30/user) | Notion AI ($10/user) | None | Brain (configurable, multi-model) |
| Real-time X/Twitter intel | No | No | No | No | **Yes (Social Pulse)** |
| Custom AI agents | Limited | Limited | No | Bots (manual) | Full agent system |
| Local LLM inference | No | No | No | No | WebLLM |
| Real-time web search in AI | No | Limited | Limited | No | **Yes (Grok built-in)** |

**The Social Pulse feature alone is a unique selling point.** No workspace/collaboration tool offers native real-time X/Twitter intelligence today.

---

## 7. Risks and Considerations

| Risk | Mitigation |
|---|---|
| xAI API stability (newer provider) | Keep OpenRouter as fallback; Grok is optional, not required |
| X data access policy changes | Social Pulse is additive; core product works without it |
| Cost of x_search at scale | Rate-limit searches per workspace; configurable intervals |
| Data privacy (data sharing program) | Make it opt-in; paid tier available without data sharing |
| Responses API is non-standard | Isolate in separate client; Chat Completions works standard |
| X content quality/spam | Grok's analysis step filters noise; configurable relevance threshold |

---

## 8. Recommended Roadmap

| Phase | Effort | Value | Priority |
|---|---|---|---|
| **Phase 1:** Surface Grok models in UI | 0 (already works) | Medium | Do now |
| **Phase 2:** Direct xAI provider | 1 day | Medium (cost savings) | Next sprint |
| **Phase 3a:** Responses API client | 2 days | High (foundation) | Next sprint |
| **Phase 3b:** `search_x` Brain tool | 1 day | High | Next sprint |
| **Phase 3c:** Social Pulse feature | 3-5 days | **Very High** (unique) | Following sprint |
| **Phase 4:** Image generation | 1 day | Low-Medium | Backlog |

**Total to full integration: ~8-10 days of engineering work.**

---

## 9. Key Technical References

- xAI API Docs: `https://docs.x.ai`
- Chat Completions: `https://docs.x.ai/docs/guides/chat`
- Function Calling: `https://docs.x.ai/docs/guides/function-calling`
- Built-in Tools: `https://docs.x.ai/docs/guides/tools/overview`
- X Search: `https://docs.x.ai/developers/tools/web-search`
- Models & Pricing: `https://docs.x.ai/developers/models`
- OpenRouter Grok: `https://openrouter.ai/provider/xai`
- Sentiment Analysis Cookbook: `https://docs.x.ai/cookbook/examples/sentiment_analysis_on_x`
