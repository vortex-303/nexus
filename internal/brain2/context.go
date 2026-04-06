package brain2

import (
	"fmt"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"
)

// CompressOldToolResults replaces tool result content older than the last
// keepRecent messages with one-line summaries. This prevents context explosion
// during multi-round tool-calling loops.
//
// Based on JetBrains research: 2.6% better accuracy at 52% lower cost.
// Recent messages kept at full fidelity so the model has what it just saw.
func CompressOldToolResults(messages []brain.Message, keepRecent int) []brain.Message {
	if keepRecent <= 0 {
		keepRecent = 6
	}
	if len(messages) <= keepRecent {
		return messages // nothing to compress
	}

	compressed := make([]brain.Message, len(messages))
	copy(compressed, messages)

	cutoff := len(messages) - keepRecent

	for i := 0; i < cutoff; i++ {
		msg := &compressed[i]

		if msg.Role == "tool" && len(msg.Content) > 200 {
			// Compress tool result to a one-line summary
			msg.Content = summarizeToolResult(msg.Content, msg.ToolCallID)
		}

		if msg.Role == "assistant" && len(msg.Content) > 500 && len(msg.ToolCalls) > 0 {
			// Keep tool calls metadata but compress the text if it's large
			if len(msg.Content) > 1000 {
				msg.Content = msg.Content[:200] + "\n[...earlier response truncated]"
			}
		}
	}

	return compressed
}

// summarizeToolResult creates a one-line summary of a tool result.
func summarizeToolResult(content, toolCallID string) string {
	// Detect tool type from content patterns
	lines := strings.Split(content, "\n")
	firstLine := ""
	if len(lines) > 0 {
		firstLine = strings.TrimSpace(lines[0])
	}

	// Count meaningful content
	charCount := len(content)
	lineCount := len(lines)

	// Try to extract a useful summary from the first line
	summary := firstLine
	if len(summary) > 150 {
		summary = summary[:150] + "..."
	}

	// Detect specific tool patterns
	switch {
	case strings.Contains(content, "Web search results"):
		resultCount := strings.Count(content, "http")
		return fmt.Sprintf("[search: %d results, %d chars] %s", resultCount, charCount, summary)

	case strings.Contains(content, "Task created") || strings.Contains(content, "task_id"):
		return fmt.Sprintf("[task action completed] %s", summary)

	case strings.Contains(content, "Document created") || strings.Contains(content, "document_id"):
		return fmt.Sprintf("[document action completed] %s", summary)

	case strings.Contains(content, "Content from http") || strings.Contains(content, "**Title:**"):
		return fmt.Sprintf("[url fetched: %d chars] %s", charCount, summary)

	case strings.Contains(content, "calendar") || strings.Contains(content, "event"):
		return fmt.Sprintf("[calendar action] %s", summary)

	case strings.Contains(content, "memory") || strings.Contains(content, "Memory"):
		return fmt.Sprintf("[memory action] %s", summary)

	default:
		return fmt.Sprintf("[tool result: %d lines, %d chars] %s", lineCount, charCount, summary)
	}
}

// InjectBudgetWarning appends a budget pressure warning to a tool result
// or creates a system-like message when the model is running low on iterations.
func InjectBudgetWarning(depth, maxDepth int) string {
	pct := float64(depth) / float64(maxDepth) * 100

	if pct >= 90 {
		return "\n\n⚠ FINAL ITERATION — You must produce your final answer NOW. No more tool calls."
	}
	if pct >= 70 {
		remaining := maxDepth - depth
		return fmt.Sprintf("\n\n⚠ Budget warning: %d iterations remaining. Start wrapping up your response.", remaining)
	}
	return ""
}
