package brain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/metrics"
)

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// MessageImage represents an image in a multimodal response.
type MessageImage struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// Message represents a chat message for the LLM.
type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Images     []MessageImage `json:"images,omitempty"`
}

// CompletionRequest is the request to OpenRouter.
type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Modalities  []string  `json:"modalities,omitempty"`
}

// CompletionChoice is a single choice in the response.
type CompletionChoice struct {
	Message Message `json:"message"`
	Delta   struct {
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

// CompletionResponse is the non-streaming response from OpenRouter.
type CompletionResponse struct {
	Choices []CompletionChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client talks to the OpenRouter API.
type Client struct {
	APIKey     string
	Model      string
	Multimodal bool
	HTTPClient *http.Client
}

// NewClient creates an OpenRouter client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		APIKey:     apiKey,
		Model:      model,
		HTTPClient: http.DefaultClient,
	}
}

// Complete sends a non-streaming completion request.
func (c *Client) Complete(systemPrompt string, messages []Message) (string, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(c.Model, "brain").Observe(time.Since(start).Seconds())
	}()
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       c.Model,
		Messages:    msgs,
		Stream:      false,
		MaxTokens:   2048,
		Temperature: 0.7,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	var result CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if result.Error != nil {
		metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "error").Inc()
		return "", fmt.Errorf("openrouter: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "error").Inc()
		return "", fmt.Errorf("no choices in response")
	}
	metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "ok").Inc()
	return result.Choices[0].Message.Content, nil
}

// CompleteMultimodal sends a completion request with multimodal output (text + images).
// Returns text content, images, and error.
func (c *Client) CompleteMultimodal(systemPrompt string, messages []Message) (string, []MessageImage, error) {
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       c.Model,
		Messages:    msgs,
		Stream:      false,
		MaxTokens:   4096,
		Temperature: 0.7,
		Modalities:  []string{"text", "image"},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	var result CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("decoding response: %w", err)
	}
	if result.Error != nil {
		return "", nil, fmt.Errorf("openrouter: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", nil, fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0]
	return choice.Message.Content, choice.Message.Images, nil
}

// CompleteWithTools sends a completion request with tool definitions.
// Returns content, tool calls, and error.
// Note: modalities cannot be combined with tools on OpenRouter, so image
// generation for multimodal models uses a separate CompleteMultimodal call.
func (c *Client) CompleteWithTools(systemPrompt string, messages []Message, tools []ToolDef) (string, []ToolCall, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(c.Model, "brain").Observe(time.Since(start).Seconds())
	}()
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       c.Model,
		Messages:    msgs,
		Stream:      false,
		MaxTokens:   2048,
		Temperature: 0.7,
		Tools:       tools,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	var result ToolCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("decoding response: %w", err)
	}
	if result.Error != nil {
		metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("openrouter: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("no choices in response")
	}
	metrics.LLMCallsTotal.WithLabelValues(c.Model, "brain", "ok").Inc()

	choice := result.Choices[0]
	return choice.Message.Content, choice.Message.ToolCalls, nil
}

// CompleteToolResults sends tool results back to the model for a final response.
func (c *Client) CompleteToolResults(systemPrompt string, messages []Message) (string, error) {
	return c.Complete(systemPrompt, messages)
}

// StreamCallback is called for each chunk of a streaming response.
type StreamCallback func(chunk string, done bool)

// CompleteStream sends a streaming completion request.
func (c *Client) CompleteStream(systemPrompt string, messages []Message, cb StreamCallback) error {
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       c.Model,
		Messages:    msgs,
		Stream:      true,
		MaxTokens:   2048,
		Temperature: 0.7,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", openRouterURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openrouter %d: %s", resp.StatusCode, string(errBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			cb("", true)
			return nil
		}

		var chunk CompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			if content != "" {
				cb(content, false)
			}
		}
	}
	cb("", true)
	return scanner.Err()
}

const embeddingURL = "https://openrouter.ai/api/v1/embeddings"
const embeddingModel = "openai/text-embedding-3-small"

// Embed returns a 1536-dimensional embedding vector for the given text.
func (c *Client) Embed(text string) ([]float32, error) {
	reqBody := map[string]any{
		"model": embeddingModel,
		"input": text,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", embeddingURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://nexus.chat")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding embedding response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("embedding error: %s", result.Error.Message)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding in response")
	}
	return result.Data[0].Embedding, nil
}
