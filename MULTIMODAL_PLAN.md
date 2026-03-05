# Nexus Multimodal Integration Plan

## Goal
Deep Gemini multimodal integration so agents can see images, analyze documents, generate high-quality images with text, edit images conversationally, and process video/audio — all flowing naturally through the existing chat UX.

---

## Current State

### What Works
- Image generation via `generate_image` tool (Gemini 2.5 Flash Image)
- File uploads stored as content-addressed blobs (SHA256)
- PDF text extraction for knowledge base
- OpenRouter for text LLM (Claude, Llama, etc.)
- Tool use / function calling loop in agent runtime

### What's Broken
- `stripBase64Images()` removes all image data before LLM calls — agents can't see images
- `Message` struct is text-only — no `parts` array for mixed content
- PDF analysis discards images and layout
- No Gemini File API integration for large files (video, audio, big PDFs)
- Image generation has no aspect ratio, resolution, or style control
- No image editing / refinement (multi-turn)
- Agent responses are fully buffered, no streaming
- No vision model selection — can't route to vision-capable models

---

## Gemini Model Lineup (March 2026)

| Model | ID | Best For | Price (input/output per 1M) |
|---|---|---|---|
| Gemini 3.1 Pro | `gemini-3.1-pro-preview` | Complex reasoning, analysis | $2.00 / $12.00 |
| Gemini 3 Flash | `gemini-3-flash-preview` | Fast general-purpose | ~$0.30 / $2.50 |
| Gemini 2.5 Flash | `gemini-2.5-flash` | Production workhorse | $0.30 / $2.50 |
| Gemini 2.5 Flash Image | `gemini-2.5-flash-image` | Image gen (production) | $0.30 / $2.50 + ~$0.039/img |
| Gemini 3 Pro Image | `gemini-3-pro-image-preview` | 4K image gen, best text | ~$2.00 / $12.00 + ~$0.10/img |

### Input Modalities (all models)
- Text, images (JPEG/PNG/WebP/GIF), video (MP4/MOV up to 1hr), audio (MP3/WAV up to 8hrs), PDFs, code
- Inline base64: up to 100MB (50MB for PDFs)
- File API: up to 2GB per file, 20GB total, 48hr TTL

### Output Modalities (image models)
- Text + images interleaved in same response
- 14 aspect ratios: 1:1, 1:4, 1:8, 2:3, 3:2, 3:4, 4:1, 4:3, 4:5, 5:4, 8:1, 9:16, 16:9, 21:9
- Resolutions: 512px, 1K, 2K, 4K (Pro only)
- Multi-turn conversational editing
- Up to 14 reference images for style transfer

### Unique Gemini Strengths
- Native multimodal (not separate DALL-E endpoint)
- 1M token context window
- Best-in-class text rendering in generated images
- Native code execution sandbox
- Grounding with Google Search
- Context caching (75% discount on cached tokens)

---

## Architecture Changes

### Phase 1: Multimodal Message Format

**File:** `internal/brain/types.go`

Replace flat `Content string` with parts-based message:

```go
type ContentPart struct {
    Type     string `json:"type"`               // "text", "image", "file"
    Text     string `json:"text,omitempty"`
    MimeType string `json:"mime_type,omitempty"`
    Data     string `json:"data,omitempty"`      // base64 for inline
    FileURI  string `json:"file_uri,omitempty"`  // for Gemini File API refs
    FileHash string `json:"file_hash,omitempty"` // Nexus blob hash
}

type Message struct {
    Role       string        `json:"role"`
    Content    string        `json:"content"`              // backward compat (text-only)
    Parts      []ContentPart `json:"parts,omitempty"`      // multimodal content
    Name       string        `json:"name,omitempty"`
    ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
    ToolCallID string        `json:"tool_call_id,omitempty"`
    Images     []MessageImage `json:"images,omitempty"`    // keep for backward compat
}
```

**Backward compatibility:** If `Parts` is empty, use `Content` as single text part. All existing code continues to work.

### Phase 2: Gemini Native Client

**File:** `internal/brain/gemini.go` (new, replaces `gemini_image.go`)

Direct Gemini API client — not through OpenRouter — for full multimodal support:

```go
type GeminiClient struct {
    APIKey     string
    HTTPClient *http.Client
}

type GeminiConfig struct {
    Model              string   // "gemini-2.5-flash", "gemini-2.5-flash-image", etc.
    SystemInstruction  string
    ResponseModalities []string // ["TEXT"], ["TEXT", "IMAGE"], ["IMAGE"]
    ImageConfig        *ImageConfig
    Tools              []ToolDef
    Temperature        float64
    MaxTokens          int
}

type ImageConfig struct {
    AspectRatio string // "16:9", "1:1", etc.
    ImageSize   string // "512px", "1K", "2K", "4K"
}
```

Methods:
- `GenerateContent(config, messages []Message) (*GeminiResponse, error)` — unified endpoint
- `GenerateContentStream(config, messages, callback) error` — SSE streaming
- `UploadFile(filePath, mimeType) (*FileRef, error)` — File API upload
- `GetFile(name) (*FileRef, error)` — poll processing state
- `DeleteFile(name) error` — cleanup

**REST endpoint:**
```
POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent
POST https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent
POST https://generativelanguage.googleapis.com/upload/v1beta/files
```

### Phase 3: Vision Input Pipeline

**File:** `internal/server/agent_runtime.go`

Stop stripping images. When building messages for LLM:

1. Scan message content for image references: `![alt](/api/workspaces/{slug}/files/{hash})`
2. Load blob from content-addressed store
3. Convert to `ContentPart{Type: "image", MimeType: mime, Data: base64}`
4. Include in message `Parts` array alongside text

```go
func (s *Server) buildMultimodalMessage(msg db.Message, slug string) brain.Message {
    parts := []brain.ContentPart{{Type: "text", Text: msg.Content}}

    // Extract image references from markdown
    for _, ref := range extractFileRefs(msg.Content) {
        blob, mime, err := s.loadBlob(slug, ref.Hash)
        if err != nil || !strings.HasPrefix(mime, "image/") {
            continue
        }
        parts = append(parts, brain.ContentPart{
            Type:     "image",
            MimeType: mime,
            Data:     base64.StdEncoding.EncodeToString(blob),
        })
    }

    return brain.Message{Role: "user", Parts: parts, Name: msg.SenderName}
}
```

**Model routing:** If message contains images, prefer vision-capable model:
- If workspace model supports vision (Gemini, Claude 3.5+, GPT-4V) → use it
- If not → use Gemini 2.5 Flash as vision fallback, keep primary model for text reasoning

### Phase 4: Enhanced Image Generation

**File:** `internal/brain/gemini.go`

Replace the basic `GenerateImageGemini()` with full multimodal generation:

```go
type ImageGenRequest struct {
    Prompt          string
    ReferenceImages []ContentPart  // up to 14 for style transfer
    AspectRatio     string         // default "1:1"
    ImageSize       string         // default "1K"
    Model           string         // default "gemini-2.5-flash-image"
    EditMode        bool           // true = edit existing image
}

type ImageGenResponse struct {
    Text   string   // description / explanation
    Images []struct {
        Data     []byte
        MimeType string
    }
}
```

**New agent tools:**

| Tool | Description | Parameters |
|---|---|---|
| `generate_image` | Create image from text prompt | prompt, aspect_ratio, size, style, model |
| `edit_image` | Edit existing image conversationally | image_hash, instructions, aspect_ratio |
| `analyze_image` | Detailed vision analysis of an image | image_hash, question |
| `analyze_document` | Extract structured data from PDF/document | file_hash, question |

**Tool definitions:**

```go
// generate_image (enhanced)
{
    Name: "generate_image",
    Description: "Generate an image from a text description. Supports aspect ratios, resolution control, and style references.",
    Parameters: {
        "prompt":        {Type: "string", Required: true},
        "aspect_ratio":  {Type: "string", Enum: ["1:1","16:9","9:16","4:3","3:4","3:2","2:3","4:5","5:4","21:9"]},
        "size":          {Type: "string", Enum: ["512px","1K","2K","4K"], Default: "1K"},
        "style":         {Type: "string", Description: "Style guidance: photorealistic, watercolor, etc."},
        "reference_hash":{Type: "string", Description: "Hash of reference image for style transfer"},
    },
}

// edit_image
{
    Name: "edit_image",
    Description: "Edit an existing image. Describe what to change — the model handles masking automatically.",
    Parameters: {
        "image_hash":    {Type: "string", Required: true},
        "instructions":  {Type: "string", Required: true},
        "aspect_ratio":  {Type: "string"},
    },
}

// analyze_image
{
    Name: "analyze_image",
    Description: "Analyze an image in detail. Can answer questions, extract text (OCR), describe content, identify objects.",
    Parameters: {
        "image_hash":    {Type: "string", Required: true},
        "question":      {Type: "string", Default: "Describe this image in detail."},
    },
}

// analyze_document
{
    Name: "analyze_document",
    Description: "Analyze a PDF or document. Understands layout, tables, charts, and images within the document.",
    Parameters: {
        "file_hash":     {Type: "string", Required: true},
        "question":      {Type: "string", Required: true},
    },
}
```

### Phase 5: Gemini File API Bridge

**File:** `internal/brain/gemini_files.go` (new)

For large files (video, audio, big PDFs) that exceed inline limits:

```go
type FileManager struct {
    client    *GeminiClient
    cache     map[string]*FileRef  // hash → Gemini file ref
    mu        sync.RWMutex
}

type FileRef struct {
    Name      string    // "files/abc123"
    URI       string    // "https://generativelanguage.googleapis.com/..."
    MimeType  string
    State     string    // "PROCESSING", "ACTIVE", "FAILED"
    ExpiresAt time.Time // 48hr TTL
}

func (fm *FileManager) EnsureUploaded(hash, mime string, data []byte) (*FileRef, error) {
    // Check cache first (avoid re-uploading)
    fm.mu.RLock()
    if ref, ok := fm.cache[hash]; ok && ref.State == "ACTIVE" && time.Now().Before(ref.ExpiresAt) {
        fm.mu.RUnlock()
        return ref, nil
    }
    fm.mu.RUnlock()

    // Upload to Gemini File API
    ref, err := fm.client.UploadFile(data, mime)
    if err != nil { return nil, err }

    // Poll until processed (videos/audio need processing time)
    for ref.State == "PROCESSING" {
        time.Sleep(2 * time.Second)
        ref, err = fm.client.GetFile(ref.Name)
        if err != nil { return nil, err }
    }

    fm.mu.Lock()
    fm.cache[hash] = ref
    fm.mu.Unlock()

    return ref, nil
}
```

### Phase 6: Agent Runtime Updates

**File:** `internal/server/agent_runtime.go`

Update the agent execution loop:

1. **Message preparation:** Use `buildMultimodalMessage()` instead of stripping images
2. **Model selection:** Route to Gemini when multimodal input/output needed
3. **Tool execution:** Handle new tools (`analyze_image`, `edit_image`, `analyze_document`)
4. **Response processing:** Parse interleaved text+image parts, save images as blobs

```go
func (s *Server) handleAgentMention(ctx context.Context, slug string, agent Agent, msg Message) {
    // Build messages with multimodal content
    messages := s.buildConversationHistory(slug, msg.ChannelID, 40)

    // Detect if conversation contains images/files
    hasVisualContent := s.conversationHasVisualContent(messages)

    // Select model based on capabilities needed
    model := s.selectModel(agent, hasVisualContent)

    // Build config
    config := GeminiConfig{
        Model:             model,
        SystemInstruction: systemPrompt,
        Temperature:       agent.Temperature,
        Tools:             agentTools,
    }

    // If agent can generate images, enable multimodal output
    if agent.HasTool("generate_image") || agent.HasTool("edit_image") {
        config.ResponseModalities = []string{"TEXT", "IMAGE"}
    }

    // Execute with tool loop
    response, err := s.executeAgentLoop(ctx, config, messages)

    // Process response parts (text + images)
    content, imageMarkdown := s.processMultimodalResponse(slug, response)

    // Send message
    s.sendAgentMessage(slug, agent, msg.ChannelID, content+imageMarkdown)
}
```

### Phase 7: Frontend Multimodal UX

**File:** `web/src/routes/(app)/w/[slug]/+page.svelte`

1. **Image paste/drop → vision analysis:** When user pastes an image and mentions an agent, the image gets sent to the LLM
2. **Image generation controls:** When agent generates an image, show aspect ratio / size controls
3. **Image editing flow:** Click generated image → "Edit with AI" → conversational refinement in thread
4. **Document analysis:** Drop PDF → agent automatically analyzes with vision
5. **Gallery view:** Multi-image responses displayed as grid with lightbox

**File:** `web/src/lib/api.ts`

```typescript
// Upload file to Gemini File API (for large files)
export async function uploadToGemini(slug: string, fileHash: string) {
    return request('POST', `/api/workspaces/${slug}/files/${fileHash}/gemini-upload`);
}
```

### Phase 8: Context Caching

**File:** `internal/brain/gemini_cache.go` (new)

For repeated queries against the same large document/codebase:

```go
type ContextCache struct {
    client *GeminiClient
    caches map[string]*CacheRef  // key → cache
}

type CacheRef struct {
    Name      string
    Model     string
    ExpiresAt time.Time
}

func (cc *ContextCache) GetOrCreate(key, model, systemPrompt string, parts []ContentPart, ttl time.Duration) (*CacheRef, error) {
    // Check existing cache
    if ref, ok := cc.caches[key]; ok && time.Now().Before(ref.ExpiresAt) {
        return ref, nil
    }

    // Create new cache
    ref, err := cc.client.CreateCache(model, systemPrompt, parts, ttl)
    if err != nil { return nil, err }

    cc.caches[key] = ref
    return ref, nil
}
```

Use cases:
- Knowledge base documents cached for repeated agent queries (75% token discount)
- Large codebase analysis across multiple questions
- Video/audio content cached for multi-question sessions

---

## Implementation Order

| Phase | What | Files | Effort |
|---|---|---|---|
| 1 | Multimodal message format | `brain/types.go` | Small |
| 2 | Gemini native client | `brain/gemini.go` (new) | Medium |
| 3 | Vision input pipeline | `server/agent_runtime.go` | Medium |
| 4 | Enhanced image gen tools | `brain/gemini.go`, `server/agent_runtime.go` | Medium |
| 5 | File API bridge | `brain/gemini_files.go` (new) | Small |
| 6 | Agent runtime updates | `server/agent_runtime.go` | Large |
| 7 | Frontend multimodal UX | `+page.svelte`, `api.ts` | Medium |
| 8 | Context caching | `brain/gemini_cache.go` (new) | Small |

**Phases 1-4 are the critical path.** They unlock vision input + enhanced image generation. Phases 5-8 are optimizations.

---

## API Examples (Go)

### Sending image for analysis
```go
resp, err := gemini.GenerateContent(GeminiConfig{
    Model: "gemini-2.5-flash",
}, []Message{{
    Role: "user",
    Parts: []ContentPart{
        {Type: "text", Text: "What's in this image?"},
        {Type: "image", MimeType: "image/jpeg", Data: base64Data},
    },
}})
```

### Generating image with aspect ratio
```go
resp, err := gemini.GenerateContent(GeminiConfig{
    Model:              "gemini-2.5-flash-image",
    ResponseModalities: []string{"TEXT", "IMAGE"},
    ImageConfig:        &ImageConfig{AspectRatio: "16:9", ImageSize: "2K"},
}, []Message{{
    Role: "user",
    Parts: []ContentPart{
        {Type: "text", Text: "A cyberpunk cityscape at night with neon signs reading 'NEXUS'"},
    },
}})
// resp.Parts contains interleaved text + base64 image data
```

### Editing an existing image
```go
resp, err := gemini.GenerateContent(GeminiConfig{
    Model:              "gemini-2.5-flash-image",
    ResponseModalities: []string{"TEXT", "IMAGE"},
}, []Message{
    {Role: "user", Parts: []ContentPart{
        {Type: "text", Text: "Here's my logo"},
        {Type: "image", MimeType: "image/png", Data: logoBase64},
    }},
    {Role: "assistant", Parts: []ContentPart{
        {Type: "text", Text: "I can see your logo. What changes would you like?"},
    }},
    {Role: "user", Parts: []ContentPart{
        {Type: "text", Text: "Change the background to dark blue and make the text gold"},
    }},
})
```

### Analyzing a PDF with vision
```go
// Upload large PDF via File API
fileRef, _ := fileManager.EnsureUploaded(hash, "application/pdf", pdfBytes)

resp, err := gemini.GenerateContent(GeminiConfig{
    Model: "gemini-2.5-flash",
}, []Message{{
    Role: "user",
    Parts: []ContentPart{
        {Type: "text", Text: "Extract all tables and charts from this report. Summarize key findings."},
        {Type: "file", MimeType: "application/pdf", FileURI: fileRef.URI},
    },
}})
```

---

## Agent Design Patterns

### Vision-Aware Brain
The Brain agent automatically analyzes images shared in conversation:
- User drops screenshot → Brain describes what it sees and offers help
- User shares error screenshot → Brain reads the error text and suggests fixes
- User shares diagram → Brain understands the architecture and answers questions

### Creative Director (Enhanced)
- `generate_image` with full aspect ratio / resolution / style control
- `edit_image` for iterative refinement in threads
- Style transfer from reference images
- Text rendering in images (logos, social media graphics, presentations)

### Document Analyst (New Agent)
- Specialized in PDF/document understanding
- Extracts tables, charts, text with layout preservation
- Compares multiple documents
- Creates summaries with visual references

### Code Reviewer with Vision
- Understands screenshots of UIs
- Compares mockup images with actual implementation
- Generates UI component code from design screenshots

---

## Verification Checklist

1. Share image in chat → @Brain → Brain describes image accurately
2. @Brain generate a 16:9 landscape of mountains → image generated at correct ratio
3. Click generated image → "Edit" → "Add a cabin by the lake" → updated image
4. Drop PDF into chat → @Brain summarize this → structured summary including charts/tables
5. Upload video → @Brain what happens at 2:30? → accurate timestamp-based answer
6. Context caching: ask 5 questions about same PDF → tokens billed at 75% discount
7. All existing text-only agent flows continue working unchanged (backward compat)
8. `go build ./cmd/nexus/` + `cd web && npm run build` pass
9. `fly deploy` succeeds
