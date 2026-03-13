# Gemini Multimodal Plan for Nexus

## Current State
- Native Gemini text API for agents (GenerateTextGemini, CompleteWithToolsGemini)
- Image generation via `generate_image` tool (text-to-image)
- Creative Director defaults to `google/gemini-3-flash-preview`

---

## Model Lineup

| Model ID | Context | Cost (in/out per 1M) | Use Case |
|---|---|---|---|
| `gemini-3.1-pro-preview` | 1M / 64k out | $2 / $12 | Best reasoning, complex tasks |
| `gemini-3-flash-preview` | 1M / 64k out | $0.50 / $3 | Fast reasoning, cost-efficient |
| `gemini-3.1-flash-lite-preview` | 1M / 64k out | $0.25 / $1.50 | Cheapest, highest throughput |
| `gemini-3-pro-image-preview` | 65k / 32k out | $2 / ~$0.134/img | Pro-quality image gen/edit |
| `gemini-3.1-flash-image-preview` | 128k / 32k out | $0.25 / ~$0.067/img | Fast image gen/edit |
| `gemini-2.5-flash` | - | - | Reasoning fallback |
| `gemini-2.5-pro` | - | - | Deep reasoning fallback |
| `gemini-2.5-flash-image` | - | - | Legacy image gen |

---

## Input Modalities

### Text
All models. Up to 1M token context on Gemini 3 series.

### Images
- **Formats**: PNG, JPEG, WEBP, HEIC, HEIF
- **Inline**: Base64 in `inlineData` with `mimeType` + `data` (under 20MB total)
- **File API**: Upload via `client.files.upload()`, reference by URI (up to 2GB)
- **Token cost**: 258 tokens per 384px tile; larger images tiled into 768x768 blocks
- **Max**: 3,600 images per request

### Video
- **Formats**: MP4, MOV, AVI, WebM, etc.
- **File API recommended** (up to 20GB paid / 2GB free)
- **Processing**: 1 FPS sampling, ~300 tokens/second
- **Duration**: Up to 1 hour standard, 3 hours low-res

### Audio
- **Formats**: WAV, MP3, AAC, OGG, FLAC
- **Token cost**: 32 tokens/second (1 min = 1,920 tokens)
- **Max**: 9.5 hours combined per prompt

### PDFs / Documents
- **Inline**: Up to 50MB / 1,000 pages
- **Native vision**: Tables, charts, diagrams, handwriting, multi-column layouts

### URLs (Native Fetching)
- **Tool**: `{"url_context": {}}` in tools array
- Gemini fetches URL content at inference time (live fetch)
- Supports HTML, JSON, PDFs, images, CSV, XML
- Up to 20 URLs per request, 34MB per URL
- **Limitation**: Cannot combine with function calling currently

---

## Output Modalities

### Text
All models, up to 64k output tokens.

### Images (Native Generation)
- **Models**: `gemini-3.1-flash-image-preview`, `gemini-3-pro-image-preview`, `gemini-2.5-flash-image`
- **Config**: `responseModalities: ['TEXT', 'IMAGE']`
- **Format**: PNG base64 in `part.inlineData`
- **Resolutions**: 512px, 1K, 2K, 4K
- **Aspect ratios**: 1:1, 1:4, 2:3, 3:2, 3:4, 4:3, 4:5, 5:4, 9:16, 16:9, 21:9, etc.
- **SynthID watermark** on all generated images

### Audio / TTS
- **Models**: `gemini-2.5-flash-tts`, `gemini-2.5-pro-tts`
- Single/multi-speaker, 24 languages, expressive style control

---

## Key Capabilities to Leverage

### 1. Image Editing (instruction-based, no mask)
Send existing image + natural language instruction → returns edited image.

```
POST generateContent
contents: [
  { parts: [{ text: "Remove the person in the background" }] },
  { parts: [{ inlineData: { mimeType: "image/jpeg", data: "<base64>" } }] }
]
generationConfig: { responseModalities: ["TEXT", "IMAGE"] }
```

**Capabilities:**
- Add/remove objects
- Change colors, materials, styles
- Background replacement/blur
- Style transfer (photo → watercolor, etc.)
- Pose alteration
- Colorization (B&W → color)
- Multi-image fusion (combine elements from multiple images)
- Multi-turn editing (iterative refinement in conversation)

**Reference image limits:**
- Flash: Up to 10 object + 4 character images
- Pro: Up to 6 object + 5 character images

### 2. Image Understanding
All Gemini models (not just image-specific) can analyze images:
- Scene description
- OCR / text extraction
- Object detection (returns bounding boxes `[ymin, xmin, ymax, xmax]` scaled 0-1000)
- Image segmentation (Gemini 2.5+)
- Chart/diagram analysis
- Multi-image comparison

### 3. URL Context (native web fetching)
```
tools: [{ "url_context": {} }]
```
- Gemini fetches and analyzes URLs at inference time
- No scraping infrastructure needed
- Supports HTML, JSON, PDFs, images
- Up to 20 URLs per request

### 4. Google Search Grounding
```
tools: [{ "google_search": {} }]
```
- Model autonomously decides when to search
- Returns `groundingMetadata` with source URIs and per-segment citations
- ~$14 per 1,000 search queries
- Great for research agents

### 5. Multimodal Function Responses (Gemini 3 only)
Tool results can include images (`image/png`, `image/jpeg`) and documents (`application/pdf`).
Model reasons over returned media — enables pipelines like:
search → fetch image → analyze → edit → return

### 6. Video Understanding
- Describe, segment, timestamp-reference, Q&A
- Combined audio+visual analysis
- Up to 1 hour at standard resolution

### 7. Audio Understanding
- Transcription and translation
- Emotion detection
- Non-speech sound recognition
- Timestamp-referenced analysis

---

## Implementation Roadmap for Nexus

### Phase A: Image Editing Tool (High Impact)
**New tool: `edit_image`** for agents with image capabilities.

- User uploads/pastes image in chat
- Agent receives image as base64 input
- Sends to Gemini image model with edit instruction
- Returns edited image as blob

**Changes needed:**
- `brain/gemini_image.go`: Add `EditImageGemini(apiKey, model, instruction string, imageData, mimeType string)` function
- `brain_tools.go`: New `edit_image` tool definition + execution
- `agents.go`: Add `edit_image` to available tools list
- Frontend: Pass uploaded images to agent context

### Phase B: Image Understanding in Chat
Let agents analyze images users upload/paste in messages.

**Changes needed:**
- `agent_runtime.go`: When building messages, detect image attachments and include as `inlineData` parts
- `brain/gemini.go`: Update `convertToGeminiContents()` to handle `Message.Images`
- Works automatically for all Gemini-model agents

### Phase C: URL Analysis
"@Creative Director analyze this competitor's landing page: https://..."

**Changes needed:**
- `brain/gemini.go`: Support `url_context` tool in Gemini requests
- `agent_runtime.go`: Detect URLs in messages, auto-enable URL context
- OR: New `analyze_url` agent tool

### Phase D: Google Search Grounding
Real-time web research for agents (especially Caly).

**Changes needed:**
- `brain/gemini.go`: Support `google_search` tool in Gemini requests
- `agents.go`: New `web_search` tool option for agents
- Surface grounding citations in frontend

### Phase E: PDF/Document Analysis
Upload a brief, contract, or report → agent extracts and acts on content.

**Changes needed:**
- Support PDF uploads in chat messages
- Pass as inline data to Gemini (same as images)
- Agent can summarize, extract action items, answer questions

### Phase F: Multi-Step Generation Pipelines
Conversational creative workflows:
1. Generate initial concept
2. User provides feedback
3. Agent edits/refines
4. Iterate until approved

**Changes needed:**
- Thread-aware image context (carry previous images through conversation)
- Multi-turn image editing state

---

## API Details

### Endpoint
```
POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={apiKey}
```

### Image Generation/Editing Request
```json
{
  "systemInstruction": { "parts": [{ "text": "system prompt" }] },
  "contents": [
    {
      "role": "user",
      "parts": [
        { "text": "Make the sky purple and add dramatic clouds" },
        { "inlineData": { "mimeType": "image/jpeg", "data": "<base64>" } }
      ]
    }
  ],
  "generationConfig": {
    "responseModalities": ["TEXT", "IMAGE"],
    "imageConfig": {
      "aspectRatio": "16:9",
      "imageSize": "2K"
    }
  }
}
```

### URL Context Request
```json
{
  "contents": [{ "parts": [{ "text": "Summarize https://example.com" }] }],
  "tools": [{ "url_context": {} }]
}
```

### Google Search Grounding Request
```json
{
  "contents": [{ "parts": [{ "text": "Latest AI news today" }] }],
  "tools": [{ "google_search": {} }]
}
```

---

## Limitations
- Image models have smaller context (65k-128k) vs text models (1M)
- URL context cannot combine with function calling
- SynthID watermark on all generated images (cannot be removed)
- Knowledge cutoff: January 2025 (use Google Search grounding for newer info)
- YouTube URL processing is preview/free tier only
