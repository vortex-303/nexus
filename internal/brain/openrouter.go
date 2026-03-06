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
	APIKey             string
	Model              string
	Multimodal         bool
	HTTPClient         *http.Client
	FreeModelFallbacks []string // additional models to try on retryable errors
}

// NewClient creates an OpenRouter client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		APIKey:     apiKey,
		Model:      model,
		HTTPClient: http.DefaultClient,
	}
}

// isRetryableError checks if an error is retryable (rate limit, overloaded, unavailable).
func isRetryableError(statusCode int, err error) bool {
	if statusCode == 429 || statusCode == 503 {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "overloaded") ||
		strings.Contains(msg, "not available") ||
		strings.Contains(msg, "capacity")
}

// Complete sends a non-streaming completion request.
// If FreeModelFallbacks is set, retries with next model on retryable errors (max 3 attempts).
func (c *Client) Complete(systemPrompt string, messages []Message) (string, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(c.Model, "brain").Observe(time.Since(start).Seconds())
	}()
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	models := []string{c.Model}
	models = append(models, c.FreeModelFallbacks...)
	maxAttempts := 3
	if len(models) < maxAttempts {
		maxAttempts = len(models)
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		model := models[i]
		req := CompletionRequest{
			Model:       model,
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
			lastErr = fmt.Errorf("openrouter request: %w", err)
			if len(c.FreeModelFallbacks) > 0 {
				continue
			}
			return "", lastErr
		}

		statusCode := resp.StatusCode
		var result CompletionResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if decodeErr != nil {
			lastErr = fmt.Errorf("decoding response: %w", decodeErr)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			return "", lastErr
		}
		if result.Error != nil {
			lastErr = fmt.Errorf("openrouter: %s", result.Error.Message)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			metrics.LLMCallsTotal.WithLabelValues(model, "brain", "error").Inc()
			return "", lastErr
		}
		if len(result.Choices) == 0 {
			metrics.LLMCallsTotal.WithLabelValues(model, "brain", "error").Inc()
			return "", fmt.Errorf("no choices in response")
		}
		metrics.LLMCallsTotal.WithLabelValues(model, "brain", "ok").Inc()
		return result.Choices[0].Message.Content, nil
	}
	return "", lastErr
}

// CompleteMultimodal sends a completion request with multimodal output (text + images).
// Returns text content, images, and error.
// If FreeModelFallbacks is set, retries with next model on retryable errors (max 3 attempts).
func (c *Client) CompleteMultimodal(systemPrompt string, messages []Message) (string, []MessageImage, error) {
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	models := []string{c.Model}
	models = append(models, c.FreeModelFallbacks...)
	maxAttempts := 3
	if len(models) < maxAttempts {
		maxAttempts = len(models)
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		model := models[i]
		req := CompletionRequest{
			Model:       model,
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
			lastErr = fmt.Errorf("openrouter request: %w", err)
			if len(c.FreeModelFallbacks) > 0 {
				continue
			}
			return "", nil, lastErr
		}

		statusCode := resp.StatusCode
		var result CompletionResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if decodeErr != nil {
			lastErr = fmt.Errorf("decoding response: %w", decodeErr)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			return "", nil, lastErr
		}
		if result.Error != nil {
			lastErr = fmt.Errorf("openrouter: %s", result.Error.Message)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			return "", nil, lastErr
		}
		if len(result.Choices) == 0 {
			return "", nil, fmt.Errorf("no choices in response")
		}

		choice := result.Choices[0]
		return choice.Message.Content, choice.Message.Images, nil
	}
	return "", nil, lastErr
}

// CompleteWithTools sends a completion request with tool definitions.
// Returns content, tool calls, and error.
// If FreeModelFallbacks is set, retries with next model on retryable errors (max 3 attempts).
func (c *Client) CompleteWithTools(systemPrompt string, messages []Message, tools []ToolDef) (string, []ToolCall, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(c.Model, "brain").Observe(time.Since(start).Seconds())
	}()
	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	models := []string{c.Model}
	models = append(models, c.FreeModelFallbacks...)
	maxAttempts := 3
	if len(models) < maxAttempts {
		maxAttempts = len(models)
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		model := models[i]
		req := CompletionRequest{
			Model:       model,
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
			lastErr = fmt.Errorf("openrouter request: %w", err)
			if len(c.FreeModelFallbacks) > 0 {
				continue
			}
			return "", nil, lastErr
		}

		statusCode := resp.StatusCode
		var result ToolCompletionResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if decodeErr != nil {
			lastErr = fmt.Errorf("decoding response: %w", decodeErr)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			return "", nil, lastErr
		}
		if result.Error != nil {
			lastErr = fmt.Errorf("openrouter: %s", result.Error.Message)
			if len(c.FreeModelFallbacks) > 0 && isRetryableError(statusCode, lastErr) {
				continue
			}
			metrics.LLMCallsTotal.WithLabelValues(model, "brain", "error").Inc()
			return "", nil, lastErr
		}
		if len(result.Choices) == 0 {
			metrics.LLMCallsTotal.WithLabelValues(model, "brain", "error").Inc()
			return "", nil, fmt.Errorf("no choices in response")
		}
		metrics.LLMCallsTotal.WithLabelValues(model, "brain", "ok").Inc()

		choice := result.Choices[0]
		return choice.Message.Content, choice.Message.ToolCalls, nil
	}
	return "", nil, lastErr
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
