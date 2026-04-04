# Gemma 4 — Multimodal Capabilities & Integration Opportunities

> Research date: 2026-04-04
> Models: E2B (2.3B active), E4B (4.5B), 26B MoE (3.8B active), 31B dense
> License: Apache 2.0 (no restrictions)

---

## 1. Vision / Image Understanding

**Status: Working on all 4 model sizes.**

| Capability | How | Quality |
|-----------|-----|---------|
| Image captioning | Send image + "describe this" | Strong |
| OCR (multilingual) | Higher token budget (560-1120) | Strong, handwriting too |
| Object detection | "detect person and car" → JSON bounding boxes | Native `{"box_2d": [y1,x1,y2,x2], "label": "..."}` |
| GUI element detection | "find the submit button" → coordinates | Works for agent automation |
| Chart/table reading | Send chart image → structured data | 85.6% MATH-Vision (31B) |
| Screenshot → HTML | Send screenshot → code generation | Works with thinking mode |
| Document parsing | PDF pages as images → extracted text | OmniDocBench competitive |

**Visual Token Budget** — configurable per request:
- 70-140: fast classification, video frames
- 280 (default): general purpose
- 560-1120: OCR, small text, documents

**API format (Ollama):**
```bash
curl http://localhost:11434/api/chat -d '{
  "model": "gemma4",
  "messages": [{"role": "user", "content": "Describe this", "images": ["<base64>"]}]
}'
```

**API format (OpenAI-compatible / OpenRouter):**
```json
{"role": "user", "content": [
  {"type": "image_url", "image_url": {"url": "https://..."}},
  {"type": "text", "text": "What's in this image?"}
]}
```

### Brain v2 Opportunities
- **Image analysis tool**: user uploads image → Brain describes, extracts text, detects objects
- **Document processing**: PDF/invoice/receipt analysis without external OCR
- **Screenshot understanding**: user shares screenshot → Brain reads UI elements
- **Chart analysis**: data visualization → structured data extraction

---

## 2. Audio Understanding

**Status: Working on E2B and E4B ONLY. 26B/31B do NOT support audio.**

| Capability | Details |
|-----------|---------|
| Speech transcription | Multilingual ASR |
| Speech translation | Direct audio → translated text |
| Audio QA | Ask questions about audio content |
| Max duration | **30 seconds** (hard limit) |
| Token cost | 25 tokens per second of audio |

**Input requirements:** 16kHz, mono, float32. MP3/WAV supported.

**Limitation:** Speech only — no music, sound effects, or environmental sounds.

### Brain v2 Opportunities
- **Voice messages**: user sends audio clip → Brain transcribes + responds
- **Meeting notes**: 30-second clips → transcription + action items
- **Multilingual support**: auto-detect language from audio

**Constraint:** 30-second limit means chunking required for longer audio. E2B/E4B only.

---

## 3. Video Understanding

**Status: Working on all 4 sizes. Up to 60 seconds at 1 FPS.**

| Capability | Details |
|-----------|---------|
| Frame analysis | Extracted at ~1 FPS, each frame through vision encoder |
| Audio track | E2B/E4B only (extracted separately) |
| Token cost | 60 frames × 280 tokens = ~16,800 tokens for 60s video |

### Brain v2 Opportunities
- **Video summaries**: user shares short video → Brain describes what happens
- **Tutorial extraction**: walkthrough video → step-by-step text instructions

**Constraint:** Memory-intensive. Practical for short clips only.

---

## 4. Tool Calling + Vision (Agentic)

**Status: Working. This is the flagship capability.**

The model can **see an image AND call tools** based on what it sees:

```
User: [uploads photo of a city] "What city is this and what's the weather?"
Model: <sees Paris> → calls get_weather(location="Paris") → "This is Paris, currently 18°C and sunny"
```

**GUI Agent pattern:**
1. Screenshot → model detects UI elements with bounding boxes
2. Model calls `click(x, y)` tool based on detected coordinates
3. New screenshot → repeat

**Thinking mode + tool calling:**
- Enable extended reasoning before tool decisions
- `thinking={"type": "enabled", "budget_tokens": 5000}`
- Model shows internal reasoning chain, then calls tools

### Brain v2 Opportunities
- **Smart image analysis**: upload receipt → Brain calls `create_task("Pay invoice $X")`
- **Visual web research**: screenshot of a page → Brain extracts data + calls tools
- **Multi-step visual workflows**: see image → plan actions → execute tools

---

## 5. Document Processing

**Status: Working. Core use case.**

| Document Type | Approach | Quality |
|--------------|----------|---------|
| PDF pages | Render as images, high token budget | Strong |
| Invoices/receipts | Image → structured JSON extraction | Strong |
| Charts/graphs | Image → data interpretation | 85.6% MATH-Vision |
| Tables | Image → markdown/JSON table | OmniDocBench competitive |
| Handwritten notes | Image → OCR transcription | Supported |

### Brain v2 Opportunities
- **Invoice processing**: drag-drop invoice → extracted line items, totals, dates
- **Document summarization**: multi-page PDF → key points extraction
- **Data entry automation**: form images → structured data

---

## 6. Notable GitHub Projects

| Project | Stars | What |
|---------|-------|------|
| `unslothai/unsloth` | 59K | Training/fine-tuning, day-0 Gemma 4 support |
| `NexaAI/nexa-sdk` | 7.9K | Run VLMs across GPU/NPU/CPU |
| `anisayari/Gemma4-frontuse` | — | React + FastAPI multimodal lab |
| `zavora-ai/zavora-omni` | — | Speech-to-speech on Gemma 4 E4B |
| `bolyki01/localllm-gemma4-mlx` | 9 | Local Gemma 4 on MLX (Apple Silicon) |
| `fabiopacifici-bot/microclaw` | — | Voice-native AI agent with E2B |
| `Kartheek-Lenka/kisanlens-ai` | — | Offline crop disease diagnosis via Ollama vision |
| `ToXMon/browsrgemma` | — | 100% client-side browser AI |
| `promptlo/ComfyUI-Gemma4-GGUF` | — | ComfyUI integration |
| `mattrob333/gemma-skills` | — | Agent skills for Google AI Edge Gallery |

---

## 7. Edge / Mobile Deployment

| Platform | Model | Performance |
|----------|-------|-------------|
| Raspberry Pi 5 | E2B | 7.6 tok/s decode |
| Qualcomm IQ8 (NPU) | E2B | 31 tok/s decode |
| Apple Silicon (MLX) | E4B | Good with TurboQuant (3.5-bit KV cache) |
| Android (AICore) | E2B/E4B | Native system integration |
| iOS (LiteRT-LM) | E2B/E4B | CPU/GPU support |
| Browser (WebGPU) | E2B | transformers.js |

---

## 8. Recommended Integration Plan for Brain v2

### Phase 1: Image Understanding (highest value, easiest)
- Add `analyze_image` tool to Brain
- User uploads image in chat → Brain sees it and responds
- Works with current Ollama + OpenRouter setup
- **Files**: Add vision message support to brain2 executor

### Phase 2: Document Processing
- PDF pages rendered as images → sent to Gemma 4
- Invoice/receipt/chart extraction
- Works via existing file upload system
- **Files**: New `process_document` tool

### Phase 3: Audio Transcription (E2B/E4B only)
- Voice message support in chat
- 30-second clips → transcription
- Requires audio upload support in frontend
- **Files**: New `transcribe_audio` tool + frontend audio recorder

### Phase 4: Vision + Tool Calling (agentic)
- Screenshot → detect elements → call tools
- "Look at this chart and create tasks for each action item"
- Combines existing tools with vision input
- **Files**: Extend executor to pass images through tool loop

---

## 9. Model Selection Guide

| Use Case | Best Model | Why |
|----------|-----------|-----|
| Brain v2 Synthesizer (cloud) | 26B MoE via OpenRouter | $0.13/M, 256K context, vision + tools |
| Brain v2 Planner (local) | E4B via Ollama | 5GB VRAM, fast, good at structured output |
| Brain v2 Reflector (local) | E2B via Ollama | 2GB VRAM, cheapest, good enough |
| Image analysis (local) | E4B via Ollama | Full vision, fits consumer GPU |
| Audio transcription (local) | E2B via Ollama | Only small models have audio |
| Document processing (cloud) | 31B via OpenRouter | Best quality for OCR/tables |
| Edge/mobile | E2B | <1.5GB, runs on phone |

---

## 10. Known Limitations

| Limitation | Impact | Workaround |
|-----------|--------|-----------|
| Audio: 30s max | Can't process long recordings | Chunk into 30s segments |
| Audio: E2B/E4B only | 26B/31B can't hear | Use E4B for audio, 26B for text |
| MoE speed: 11 tok/s | Slower than expected | Use cloud (OpenRouter) for speed-critical |
| VRAM: 31B at 256K = 40GB | Can't run full context locally | Use 26B MoE or reduce context |
| Tool format: custom tokens | Not OpenAI-compatible raw | Ollama/vLLM translate automatically |
| Fine-tuning: broken at launch | Can't customize yet | Wait for transformers fix |
| Video: memory-intensive | Short clips only | Keep under 30s, low token budget |
