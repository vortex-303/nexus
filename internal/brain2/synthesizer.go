package brain2

import (
	"fmt"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"
)

// RunSynthesizer takes the original context + tool results and produces the final response.
func RunSynthesizer(cfg PipelineConfig, plan Plan, results []StepResult) string {
	// Check if any result is a direct final response (from self-correction loop)
	for _, r := range results {
		if r.Tool == "_response" {
			return r.Result
		}
	}

	// Build a summary of what was done and what was found
	var resultContext strings.Builder
	resultContext.WriteString("\n\n---\nTool execution results:\n")
	for _, r := range results {
		if r.Error != "" {
			resultContext.WriteString(fmt.Sprintf("[%s] ERROR: %s\n", r.Tool, r.Error))
		} else {
			resultContext.WriteString(fmt.Sprintf("[%s] %s\n", r.Tool, truncateResult(r.Result, 4000)))
		}
	}

	// Append tool results to the system prompt for synthesis
	synthPrompt := cfg.SystemPrompt + resultContext.String()

	// Cap system prompt
	if len(synthPrompt) > 100000 {
		synthPrompt = synthPrompt[:100000]
	}

	response, _, err := cfg.Client.Complete(synthPrompt, cfg.Messages)
	if err != nil {
		// Fallback: return raw tool results if synthesis fails
		var fallback strings.Builder
		for _, r := range results {
			if r.Result != "" {
				fallback.WriteString(r.Result)
				fallback.WriteString("\n")
			}
		}
		if fallback.Len() > 0 {
			return fallback.String()
		}
		return "Sorry, I encountered an error synthesizing the results."
	}

	return response
}

// BuildToolResultMessages converts step results to brain.Message format
// for appending to the conversation history.
func BuildToolResultMessages(results []StepResult) []brain.Message {
	var msgs []brain.Message
	for _, r := range results {
		if r.Tool == "_response" {
			continue
		}
		content := r.Result
		if r.Error != "" {
			content = r.Error
		}
		msgs = append(msgs, brain.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: r.StepID,
		})
	}
	return msgs
}

func truncateResult(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "\n[...truncated]"
	}
	return s
}
