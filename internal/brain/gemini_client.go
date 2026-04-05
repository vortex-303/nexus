package brain

import "strings"

// GeminiClient wraps the Gemini API functions to satisfy the brainCompleter interface.
// This allows Gemini to route through makeBrainClient() like OpenRouter, Ollama, and xAI.
type GeminiClient struct {
	APIKey string
	Model  string
}

// NewGeminiClient creates a client for the Google AI / Gemini API.
func NewGeminiClient(apiKey, model string) *GeminiClient {
	// Strip "google/" prefix if present (UI stores as google/gemini-... but API wants gemini-...)
	model = strings.TrimPrefix(model, "google/")
	return &GeminiClient{APIKey: apiKey, Model: model}
}

// Complete calls Gemini's generateContent for text-only completion.
func (c *GeminiClient) Complete(systemPrompt string, messages []Message) (string, *CompletionUsage, error) {
	return GenerateTextGemini(c.APIKey, c.Model, systemPrompt, messages)
}

// CompleteWithTools calls Gemini's generateContent with function calling support.
func (c *GeminiClient) CompleteWithTools(systemPrompt string, messages []Message, tools []ToolDef) (string, []ToolCall, *CompletionUsage, error) {
	return CompleteWithToolsGemini(c.APIKey, c.Model, systemPrompt, messages, tools)
}

// IsGeminiModel returns true if the model ID is a Google/Gemini/Gemma model.
func IsGeminiModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "google/gemini") ||
		strings.HasPrefix(lower, "google/gemma") ||
		strings.HasPrefix(lower, "gemini-") ||
		strings.HasPrefix(lower, "gemma-")
}

// GeminiTextModels are the models available via Google AI API for text/tool use.
var GeminiTextModels = []struct {
	ID          string
	DisplayName string
	Free        bool
}{
	{"google/gemma-4-26b-a4b-it", "Gemma 4 26B MoE (free)", true},
	{"google/gemma-4-31b-it", "Gemma 4 31B (free)", true},
	{"google/gemini-2.5-flash", "Gemini 2.5 Flash", false},
	{"google/gemini-2.5-flash-lite", "Gemini 2.5 Flash Lite", false},
	{"google/gemini-2.5-pro", "Gemini 2.5 Pro", false},
	{"google/gemini-3-flash-preview", "Gemini 3 Flash", false},
}

// GeminiImageModels are the models available for image generation via Google AI API.
var GeminiImageModels = []struct {
	ID          string
	DisplayName string
}{
	{"gemini-2.5-flash-image", "Gemini 2.5 Flash Image"},
	{"gemini-3.1-flash-image-preview", "Gemini 3.1 Flash Image"},
	{"gemini-3-pro-image-preview", "Gemini 3 Pro Image"},
	{"imagen-4.0-generate-001", "Imagen 4.0"},
}
