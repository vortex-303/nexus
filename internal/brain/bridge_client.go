package brain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexus-chat/nexus/internal/metrics"
)

// BridgeClient routes LLM requests through a WebSocket bridge to a remote Ollama instance.
// The Forward function is injected by the server — it sends the request through the bridge
// and returns the raw JSON response bytes.
type BridgeClient struct {
	Model   string
	Forward func(req CompletionRequest) ([]byte, error)
}

// NewBridgeClientFromConn creates a BridgeClient with the given forward function.
func NewBridgeClientFromConn(model string, forward func(CompletionRequest) ([]byte, error)) *BridgeClient {
	return &BridgeClient{Model: model, Forward: forward}
}

// Complete sends a non-streaming completion request through the bridge.
func (bc *BridgeClient) Complete(systemPrompt string, messages []Message) (string, *CompletionUsage, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(bc.Model, "brain").Observe(time.Since(start).Seconds())
	}()

	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       bc.Model,
		Messages:    msgs,
		Stream:      false,
		MaxTokens:   2048,
		Temperature: 0.7,
	}

	respBytes, err := bc.Forward(req)
	if err != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("bridge forward: %w", err)
	}

	var result CompletionResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("bridge decode: %w", err)
	}
	if result.Error != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("ollama: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, fmt.Errorf("no choices in bridge response")
	}

	metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "ok").Inc()
	return result.Choices[0].Message.Content, result.Usage, nil
}

// CompleteWithTools sends a tool-calling completion request through the bridge.
func (bc *BridgeClient) CompleteWithTools(systemPrompt string, messages []Message, tools []ToolDef) (string, []ToolCall, *CompletionUsage, error) {
	start := time.Now()
	defer func() {
		metrics.LLMLatency.WithLabelValues(bc.Model, "brain").Observe(time.Since(start).Seconds())
	}()

	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	req := CompletionRequest{
		Model:       bc.Model,
		Messages:    msgs,
		Stream:      false,
		MaxTokens:   2048,
		Temperature: 0.7,
		Tools:       tools,
	}

	respBytes, err := bc.Forward(req)
	if err != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, nil, fmt.Errorf("bridge forward: %w", err)
	}

	var result ToolCompletionResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, nil, fmt.Errorf("bridge decode: %w", err)
	}
	if result.Error != nil {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, nil, fmt.Errorf("ollama: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "error").Inc()
		return "", nil, nil, fmt.Errorf("no choices in bridge response")
	}

	metrics.LLMCallsTotal.WithLabelValues(bc.Model, "brain", "ok").Inc()
	choice := result.Choices[0]
	return choice.Message.Content, choice.Message.ToolCalls, result.Usage, nil
}
