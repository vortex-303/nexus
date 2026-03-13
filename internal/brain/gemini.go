package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GeminiTextRequest is the request body for Gemini generateContent (text mode).
type GeminiTextRequest struct {
	SystemInstruction *GeminiContent         `json:"systemInstruction,omitempty"`
	Contents          []GeminiContent        `json:"contents"`
	Tools             []GeminiToolDecl       `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig      `json:"toolConfig,omitempty"`
	GenerationConfig  *GeminiTextGenConfig   `json:"generationConfig,omitempty"`
}

type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type GeminiFunctionCallingConfig struct {
	Mode string `json:"mode"` // AUTO, ANY, NONE
}

type GeminiTextGenConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int  `json:"maxOutputTokens,omitempty"`
}

type GeminiToolDecl struct {
	FunctionDeclarations []GeminiFuncDecl `json:"functionDeclarations"`
}

type GeminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// GeminiUsageMetadata contains token counts from a Gemini response.
type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// GeminiTextResponse is the response from Gemini generateContent (text mode).
type GeminiTextResponse struct {
	Candidates []struct {
		Content struct {
			Parts []GeminiResponsePart `json:"parts"`
			Role  string               `json:"role"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

type GeminiResponsePart struct {
	Text             string              `json:"text,omitempty"`
	FunctionCall     *GeminiFunctionCall `json:"functionCall,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
}

type GeminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// geminiUsageToCompletion converts Gemini usage metadata to CompletionUsage.
func geminiUsageToCompletion(u *GeminiUsageMetadata) *CompletionUsage {
	if u == nil {
		return nil
	}
	return &CompletionUsage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount,
		TotalTokens:      u.TotalTokenCount,
	}
}

// GenerateTextGemini calls the Gemini API for text completion.
func GenerateTextGemini(apiKey, model, systemPrompt string, messages []Message) (string, *CompletionUsage, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, model, apiKey)

	req := GeminiTextRequest{
		Contents: convertToGeminiContents(messages),
	}
	if systemPrompt != "" {
		req.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: systemPrompt}},
		}
	}

	respBody, err := doGeminiRequest(url, req)
	if err != nil {
		return "", nil, err
	}

	var result GeminiTextResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("decoding gemini response: %w", err)
	}
	if result.Error != nil {
		return "", nil, fmt.Errorf("gemini: %s (code %d)", result.Error.Message, result.Error.Code)
	}
	if len(result.Candidates) == 0 {
		return "", nil, fmt.Errorf("no candidates in gemini response")
	}

	var textParts []string
	for _, part := range result.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}
	return strings.Join(textParts, ""), geminiUsageToCompletion(result.UsageMetadata), nil
}

// CompleteWithToolsGemini calls the Gemini API with function calling support.
func CompleteWithToolsGemini(apiKey, model, systemPrompt string, messages []Message, tools []ToolDef) (string, []ToolCall, *CompletionUsage, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, model, apiKey)

	req := GeminiTextRequest{
		Contents: convertToGeminiContents(messages),
	}
	if systemPrompt != "" {
		req.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: systemPrompt}},
		}
	}

	// Convert tools to Gemini function declarations
	if len(tools) > 0 {
		var decls []GeminiFuncDecl
		for _, t := range tools {
			decls = append(decls, GeminiFuncDecl{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			})
		}
		req.Tools = []GeminiToolDecl{{FunctionDeclarations: decls}}
		req.ToolConfig = &GeminiToolConfig{
			FunctionCallingConfig: &GeminiFunctionCallingConfig{Mode: "AUTO"},
		}
	}

	respBody, err := doGeminiRequest(url, req)
	if err != nil {
		return "", nil, nil, err
	}

	var result GeminiTextResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, nil, fmt.Errorf("decoding gemini response: %w", err)
	}
	if result.Error != nil {
		return "", nil, nil, fmt.Errorf("gemini: %s (code %d)", result.Error.Message, result.Error.Code)
	}
	if len(result.Candidates) == 0 {
		return "", nil, nil, fmt.Errorf("no candidates in gemini response")
	}

	var textParts []string
	var toolCalls []ToolCall
	for i, part := range result.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			tc := ToolCall{
				ID:   fmt.Sprintf("gemini_call_%d", i),
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
				ThoughtSignature: part.ThoughtSignature,
			}
			toolCalls = append(toolCalls, tc)
		}
	}

	return strings.Join(textParts, ""), toolCalls, geminiUsageToCompletion(result.UsageMetadata), nil
}

// convertToGeminiContents maps brain.Message slice to Gemini content format.
// Handles function calling protocol: assistant messages with ToolCalls become model messages
// with functionCall parts, and tool result messages become user messages with functionResponse parts.
func convertToGeminiContents(messages []Message) []GeminiContent {
	var contents []GeminiContent
	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		// Skip system messages (handled via systemInstruction)
		if role == "system" {
			continue
		}
		// Tool results → functionResponse parts (role must be "user" per Gemini API)
		if role == "tool" {
			// Find the tool name from ToolCallID by scanning previous messages
			toolName := m.ToolCallID
			for _, prev := range messages {
				for _, tc := range prev.ToolCalls {
					if tc.ID == m.ToolCallID {
						toolName = tc.Function.Name
						break
					}
				}
			}
			respJSON, _ := json.Marshal(map[string]string{"result": m.Content})
			contents = append(contents, GeminiContent{
				Role: "user",
				Parts: []GeminiPart{{
					FunctionResponse: &GeminiFunctionResp{
						Name:     toolName,
						Response: respJSON,
					},
				}},
			})
			continue
		}
		// Assistant messages with tool calls → model message with functionCall parts
		if role == "model" && len(m.ToolCalls) > 0 {
			var parts []GeminiPart
			if m.Content != "" {
				parts = append(parts, GeminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				p := GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: tc.Function.Name,
						Args: json.RawMessage(tc.Function.Arguments),
					},
				}
				if tc.ThoughtSignature != "" {
					p.ThoughtSignature = tc.ThoughtSignature
				}
				parts = append(parts, p)
			}
			contents = append(contents, GeminiContent{
				Role:  "model",
				Parts: parts,
			})
			continue
		}
		contents = append(contents, GeminiContent{
			Role:  role,
			Parts: []GeminiPart{{Text: m.Content}},
		})
	}
	return contents
}

func doGeminiRequest(url string, reqBody any) ([]byte, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling gemini request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading gemini response: %w", err)
	}

	return respBody, nil
}
