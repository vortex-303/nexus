package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ImageConfig holds the OpenRouter image-generation parameters.
type ImageConfig struct {
	AspectRatio string `json:"aspect_ratio,omitempty"` // "1:1", "16:9", "9:16", "4:3", "3:4"
	ImageSize   string `json:"image_size,omitempty"`   // "0.5K", "1K", "2K", "4K"
}

type imageGenRequest struct {
	Model       string       `json:"model"`
	Messages    []Message    `json:"messages"`
	Modalities  []string     `json:"modalities"`
	ImageConfig *ImageConfig `json:"image_config,omitempty"`
}

type imageGenResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Images  []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
	Usage *CompletionUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// GenerateImageOpenRouter generates an image via OpenRouter's chat
// completions endpoint with modalities=["image","text"]. Returns the
// optional text description, base64 image data (no data URL prefix),
// mime type, usage, and error.
//
// model should be an OpenRouter image-capable model ID like
// "google/gemini-3.1-flash-image-preview" (Nano Banana 2) or
// "google/gemini-3-pro-image-preview" (Nano Banana Pro).
//
// aspectRatio is one of OpenRouter's supported ratios. Empty defaults
// to "1:1" upstream.
//
// Why this exists alongside GenerateImageGemini: lets us route image
// gen through OpenRouter (one provider, unified billing, same API key
// as the rest of Brain) instead of requiring a separate Gemini key.
// GenerateImageGemini stays as a fallback for workspaces that
// explicitly choose the direct Gemini path.
func GenerateImageOpenRouter(apiKey, model, prompt, aspectRatio string) (text, imageData, mimeType string, usage *CompletionUsage, err error) {
	if apiKey == "" {
		return "", "", "", nil, fmt.Errorf("openrouter api key required")
	}
	if model == "" {
		// Default to Nano Banana 2 on OR (matches our DefaultGeminiImageModel
		// for consistency, just routed through OpenRouter instead of direct).
		model = "google/" + DefaultGeminiImageModel
	} else if !strings.Contains(model, "/") {
		// Bare model ID — prepend google/ so the same picker entries
		// ("gemini-3.1-flash-image-preview") work for both providers.
		model = "google/" + model
	}

	req := imageGenRequest{
		Model: model,
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
		Modalities: []string{"image", "text"},
	}
	if aspectRatio != "" {
		req.ImageConfig = &ImageConfig{AspectRatio: aspectRatio}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", "", "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("reading response: %w", err)
	}

	var result imageGenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", "", nil, fmt.Errorf("decoding response: %w (body: %s)", err, truncate(string(respBody), 500))
	}
	if result.Error != nil {
		return "", "", "", nil, fmt.Errorf("openrouter: %s (code %d)", result.Error.Message, result.Error.Code)
	}
	if len(result.Choices) == 0 {
		return "", "", "", nil, fmt.Errorf("no choices in response")
	}
	msg := result.Choices[0].Message
	if len(msg.Images) == 0 {
		return "", "", "", nil, fmt.Errorf("no images in response (model returned text only: %q)", truncate(msg.Content, 200))
	}

	// Parse the data URL: "data:image/png;base64,iVBORw0..."
	dataURL := msg.Images[0].ImageURL.URL
	if !strings.HasPrefix(dataURL, "data:") {
		return "", "", "", nil, fmt.Errorf("unexpected image URL format (expected data: URL, got %s)", truncate(dataURL, 80))
	}
	semicolon := strings.Index(dataURL, ";")
	comma := strings.Index(dataURL, ",")
	if semicolon < 5 || comma < semicolon {
		return "", "", "", nil, fmt.Errorf("malformed data URL: %s", truncate(dataURL, 80))
	}
	mimeType = dataURL[len("data:"):semicolon]
	imageData = dataURL[comma+1:]

	return strings.TrimSpace(msg.Content), imageData, mimeType, result.Usage, nil
}

// truncate is a local helper that mirrors the one in server/, kept here
// so this file has no upward dependencies.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
