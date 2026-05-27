# UI Cleanup — OpenRouter-only, Qwen3.5-Flash default

> Cleanup pass to simplify the product around a single engine (OpenRouter)
> and a single multimodal default model (`qwen/qwen3.5-flash-02-23`).
> Hides Claude Managed Agents (Brain v3) UI behind a feature flag without
> deleting code. Saved 2026-05-26.

## Goals

1. Default new workspaces to **Qwen3.5-Flash** (text + image multimodal, tool calling, 1M ctx, $0.065/$0.26 per M tokens)
2. Hide every UI affordance that lets users **switch into** Brain v3 (Claude Managed Agents)
3. Keep all v3 code paths intact — existing v3 workspaces stay functional
4. Optimize the chat UX around image attachments (the multimodal angle)
5. No deletions; all changes reversible by flipping `BRAIN_V3_ENABLED` back to `true`

## Why these changes (the product thesis)

- Solo dev needs a coherent product surface. Two engines = two surface areas to test, document, and support. One engine + one multimodal default = focus.
- Qwen3.5-Flash is the cheapest tool-capable native-multimodal model on OpenRouter today. Pricing makes Free tier viable at meaningful usage.
- v3 (Claude Managed Agents) costs ~15× more per token and is a different mental model. Keep the code for power users / future enterprise tier; hide from default UX.
- Multimodal-first messaging is a strong differentiator vs "self-hosted Slack." Image input on every reply, no extra setup, no model swap.

## Shipped in this commit (Phase 1 cleanup)

| Change | Where | Effect |
|---|---|---|
| `BRAIN_V3_ENABLED = false` flag | `web/src/lib/flags.ts` | One-line revert if needed |
| Default model = `qwen/qwen3.5-flash-02-23` (backend) | `internal/server/workspace.go:204` | New workspaces use Qwen |
| Default model = `qwen/qwen3.5-flash-02-23` (frontend) | `web/src/routes/(app)/w/[slug]/+page.svelte` | UI state, settings fallback, wizard default |
| Qwen3.5-Flash first in curated list | `+page.svelte:openRouterRecommended` | Model picker shows it on top with ⭐ default tag |
| Engine selector hidden under flag | `+page.svelte:~7170` | When flag off: shows "Engine: OpenRouter" status line instead of OpenRouter/Claude dual-card |
| Wizard already OpenRouter-only | Pre-existing (commit `2362d1d`) | No code change needed |

## Multimodal capability — what works now vs deferred

| Capability | Status | Path |
|---|---|---|
| **Image input on @Brain** | ✅ Works today | Existing `internal/brain/openrouter.go` `image_url` content blocks. Qwen reads them as vision input automatically. |
| **Image generation by Brain** | ✅ Works today | Via `generate_image` tool (Nano Banana 2). Independent of base model. |
| **Brain replies with images** | ✅ Works today | `CompleteMultimodal` path; `saveAgentImages` decodes base64 → blob. |
| **Video input** | ❌ Not yet | OpenRouter's standard API doesn't pass video through `image_url`. Qwen's docs claim video, but the OR protocol surface is image-only today. **Phase 2**: extract frames client-side OR wait for OR `video_url` content type. |
| **Video generation** | ❌ Not yet | Out of scope for Brain — separate product (Runway, Pika, Sora). Could MCP-integrate. **Phase 3**. |

## Deferred (next cleanup passes)

### Phase 2 — Pruning the inactive code paths from sight (still keeping code)

These surfaces still appear in the UI today only when a workspace has `brain_version === 'v3'`. Since the flag prevents anyone from entering v3, these only show for legacy v3 workspaces:

- v3 Memory tab (Brain Settings → Memory when brain_version=v3)
- v3 Skills tab (Anthropic-uploaded skills view)
- "Reset v3 agent" button
- v3 system prompt template selector
- Anthropic API key input

**Recommendation:** leave them alive for legacy workspaces; they're hidden by the existing `brainVersion === 'v3'` guards on every block. No code change needed unless we decide to force-migrate v3 workspaces back to v2 (separate decision).

### Phase 3 — Multimodal product polish

- **Image upload affordance in chat composer** — drag-drop + paste already works (via file upload). Verify chip preview shows BEFORE send.
- **"Brain can see images" hint** — a one-time tooltip on the first attachment upload
- **Image-aware quick prompts** — when a message has an image attached, slash menu surfaces `/describe`, `/extract-text`, `/analyze`
- **Vision-first persona variant** — a "Visual Analyst" persona pre-tuned for image breakdown (extends Persona Phase 2 work)
- **Cost meter** — show per-message estimated cost since Qwen is cheap, this is a positive signal

### Phase 4 — Video story (when OpenRouter or Qwen exposes it)

- **Frame extraction** client-side: when user attaches a `.mp4`/`.mov`/`.webm`, extract N frames (e.g. 1 per 2 seconds, capped at 16 frames), upload each as image_url. Brain analyzes the sequence.
- **YouTube URL ingestion** — fetch transcript + thumbnail via existing fetch tools, send transcript as text + thumbnail as image.
- **Video generation MCP** — wire Runway / Pika / Sora as MCP servers, expose via `/video` slash command.

## Reverting

To turn v3 UI back on:

```ts
// web/src/lib/flags.ts
export const BRAIN_V3_ENABLED = true;
```

That's it. Backend already supports v3 and existing v3 workspaces never stopped working.

## Pricing verification (sources)

- Qwen3.5-Flash on OpenRouter: $0.065/M input, $0.26/M output (35% off current promo)
- 1M context, 65K max output, tool calling, vision input
- Architecture: hybrid linear-attention + sparse MoE
- Released 2026-02-25
- Source: <https://openrouter.ai/qwen/qwen3.5-flash-02-23>
- Tool calling support confirmed: <https://openrouter.ai/docs/guides/features/tool-calling>

vs current default DeepSeek V4 Flash ($0.14/$0.28): Qwen is **~54% cheaper on input**, similar on output, AND multimodal. Strictly better default.
