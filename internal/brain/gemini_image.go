package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
// DefaultGeminiImageModel is the workspace default for image generation.
// Gemini 3.1 Flash Image — codename "Nano Banana 2" — is Google's
// price/performance pick: ~$0.045–$0.151 per image (512px–4K), 4K output,
// 14 aspect ratios, real-world knowledge from web search, can render text
// on the image. Replaced "gemini-2.5-flash-image" (Nano Banana 1) as the
// default in 2026; legacy still selectable via the dropdown.
const DefaultGeminiImageModel = "gemini-3.1-flash-image-preview"

// GeminiImageRequest is the request to Gemini generateContent API.
type GeminiImageRequest struct {
	Contents         []GeminiContent         `json:"contents"`
	GenerationConfig GeminiGenerationConfig  `json:"generationConfig"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             string                `json:"text,omitempty"`
	InlineData       *GeminiInlineData     `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResp   `json:"functionResponse,omitempty"`
	ThoughtSignature string                `json:"thoughtSignature,omitempty"`
}

type GeminiFunctionResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type GeminiGenerationConfig struct {
	ResponseModalities []string           `json:"responseModalities"`
	ImageConfig        *GeminiImageConfig `json:"imageConfig,omitempty"`
}

// GeminiImageConfig configures image-generation outputs. AspectRatio takes
// values like "1:1", "16:9", "9:16" — Nano Banana 2 supports 14 ratios; we
// expose the common 7 in our tool schema.
type GeminiImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
}

// GeminiImageResponse is the response from Gemini generateContent API.
type GeminiImageResponse struct {
	Candidates []struct {
		Content struct {
			Parts []GeminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// GenerateImageGemini calls the Gemini API to generate an image from a text prompt.
// aspectRatio is optional ("" → model default). Returns text description, base64
// image data, mime type, and error.
func GenerateImageGemini(apiKey, model, prompt, aspectRatio string) (text string, imageData string, mimeType string, err error) {
	if model == "" {
		model = DefaultGeminiImageModel
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, model, apiKey)

	cfg := GeminiGenerationConfig{
		ResponseModalities: []string{"TEXT", "IMAGE"},
	}
	if aspectRatio != "" {
		cfg.ImageConfig = &GeminiImageConfig{AspectRatio: aspectRatio}
	}

	reqBody := GeminiImageRequest{
		Contents: []GeminiContent{{
			Parts: []GeminiPart{{Text: prompt}},
		}},
		GenerationConfig: cfg,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", "", fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return "", "", "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", fmt.Errorf("reading response: %w", err)
	}

	var result GeminiImageResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", "", fmt.Errorf("decoding response: %w", err)
	}
	if result.Error != nil {
		return "", "", "", fmt.Errorf("gemini: %s (code %d)", result.Error.Message, result.Error.Code)
	}
	if len(result.Candidates) == 0 {
		return "", "", "", fmt.Errorf("no candidates in response")
	}

	var textParts []string
	for _, part := range result.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.InlineData != nil && part.InlineData.Data != "" {
			imageData = part.InlineData.Data
			mimeType = part.InlineData.MimeType
		}
	}

	text = ""
	for _, t := range textParts {
		if text != "" {
			text += "\n"
		}
		text += t
	}

	if imageData == "" {
		return text, "", "", fmt.Errorf("no image in response (text: %s)", text)
	}

	return text, imageData, mimeType, nil
}
