package brain2

import (
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
)

// RunSynthesizer takes the original context + tool results and produces the
// final response, streaming token deltas to cfg.OnTextDelta when available.
// The returned usage is nil when no LLM call was made (direct _response) or
// when the provider didn't report usage.
func RunSynthesizer(cfg PipelineConfig, plan Plan, results []StepResult) (string, *brain.CompletionUsage) {
	// Check if any result is a direct final response (from self-correction loop)
	for _, r := range results {
		if r.Tool == "_response" {
			return r.Result, nil
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

	llmStart := time.Now()
	response, usage, err := completeMaybeStream(cfg, synthPrompt)
	cfg.Trace.AddLLMCall(cfg.Model, time.Since(llmStart), errString(err))
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
			return fallback.String(), usage
		}
		return "Sorry, I encountered an error synthesizing the results.", usage
	}

	return response, usage
}

func truncateResult(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "\n[...truncated]"
	}
	return s
}
