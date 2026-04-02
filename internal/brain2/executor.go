package brain2

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
)

// RunExecutor executes the planned steps with parallel execution, validation,
// and a self-correction loop (up to MaxDepth iterations).
func RunExecutor(cfg PipelineConfig, plan Plan) ([]StepResult, []string) {
	if len(plan.Steps) > 0 {
		return executePlan(cfg, plan)
	}
	// No pre-planned steps — use self-correction loop with LLM tool calling
	return executeSelfCorrectingLoop(cfg, plan)
}

// executePlan runs pre-planned steps with dependency-aware parallelism.
func executePlan(cfg PipelineConfig, plan Plan) ([]StepResult, []string) {
	completed := make(map[string]StepResult)
	pending := make([]Step, len(plan.Steps))
	copy(pending, plan.Steps)
	var toolsUsed []string

	for len(pending) > 0 {
		// Find steps with all dependencies met
		var ready []Step
		var notReady []Step
		for _, step := range pending {
			allMet := true
			for _, dep := range step.DependsOn {
				if _, ok := completed[dep]; !ok {
					allMet = false
					break
				}
			}
			if allMet {
				ready = append(ready, step)
			} else {
				notReady = append(notReady, step)
			}
		}

		if len(ready) == 0 {
			break // deadlock — deps can't be met
		}

		// Execute ready steps in parallel
		results := make([]StepResult, len(ready))
		var wg sync.WaitGroup
		for i, step := range ready {
			wg.Add(1)
			go func(i int, s Step) {
				defer wg.Done()
				results[i] = executeStep(cfg, s)
			}(i, step)
		}
		wg.Wait()

		for i, step := range ready {
			completed[step.ID] = results[i]
			toolsUsed = append(toolsUsed, step.Tool)
		}
		pending = notReady
	}

	// Collect results in order
	var allResults []StepResult
	for _, step := range plan.Steps {
		if r, ok := completed[step.ID]; ok {
			allResults = append(allResults, r)
		}
	}
	return allResults, toolsUsed
}

// executeStep runs a single tool call with timeout and validation.
func executeStep(cfg PipelineConfig, step Step) StepResult {
	start := time.Now()

	call := brain.ToolCall{
		ID:   step.ID,
		Type: "function",
	}
	call.Function.Name = step.Tool
	call.Function.Arguments = step.Args

	// Validate before executing
	if err := ValidateToolCall(call, cfg.AllTools); err != nil {
		return StepResult{
			StepID:  step.ID,
			Tool:    step.Tool,
			Error:   err.String(),
			Elapsed: time.Since(start),
		}
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resultCh := make(chan string, 1)
	go func() {
		resultCh <- cfg.ExecuteTool(cfg.Slug, cfg.ChannelID, "", call)
	}()

	select {
	case result := <-resultCh:
		return StepResult{
			StepID:  step.ID,
			Tool:    step.Tool,
			Result:  result,
			Elapsed: time.Since(start),
		}
	case <-ctx.Done():
		return StepResult{
			StepID:  step.ID,
			Tool:    step.Tool,
			Error:   fmt.Sprintf("tool timed out after 30s"),
			Elapsed: time.Since(start),
		}
	}
}

// executeSelfCorrectingLoop mirrors v1's proven tool-calling pattern but adds
// validation, timeouts, and multi-round self-correction.
func executeSelfCorrectingLoop(cfg PipelineConfig, plan Plan) ([]StepResult, []string) {
	scopedTools := ScopeTools(cfg.AllTools, plan.ScopedTools)

	var allResults []StepResult
	var toolsUsed []string

	// Round 1: CompleteWithTools (same as v1)
	responseContent, toolCalls, _, err := cfg.Client.CompleteWithTools(cfg.SystemPrompt, cfg.Messages, scopedTools)
	if err != nil {
		fmt.Printf("[brain2] executor CompleteWithTools error: %v\n", err)
		// Fallback: plain completion
		plainResp, _, plainErr := cfg.Client.Complete(cfg.SystemPrompt, cfg.Messages)
		fmt.Printf("[brain2] fallback Complete: err=%v len=%d\n", plainErr, len(plainResp))
		if plainErr == nil && plainResp != "" {
			allResults = append(allResults, StepResult{
				StepID: "fallback_0", Tool: "_response", Result: plainResp,
			})
		}
		return allResults, toolsUsed
	}

	// No tool calls — model answered directly (this is fine, same as v1)
	if len(toolCalls) == 0 {
		if responseContent != "" {
			allResults = append(allResults, StepResult{
				StepID: "direct_0", Tool: "_response", Result: responseContent,
			})
		}
		return allResults, toolsUsed
	}

	// Execute tool calls with validation and timeout
	assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
	followUp := make([]brain.Message, len(cfg.Messages))
	copy(followUp, cfg.Messages)
	followUp = append(followUp, assistantMsg)

	for _, call := range toolCalls {
		// Validate before executing
		if vErr := ValidateToolCall(call, scopedTools); vErr != nil {
			fmt.Printf("[brain2] validation error for %s: %s\n", call.Function.Name, vErr.Error)
			followUp = append(followUp, brain.Message{
				Role: "tool", Content: vErr.String(), ToolCallID: call.ID,
			})
			continue
		}

		result := executeWithTimeout(cfg, call)
		toolsUsed = append(toolsUsed, call.Function.Name)
		allResults = append(allResults, StepResult{
			StepID: call.ID, Tool: call.Function.Name, Result: result,
		})
		followUp = append(followUp, brain.Message{
			Role: "tool", Content: result, ToolCallID: call.ID,
		})
	}

	// Round 2+: self-correction loop (v2 improvement over v1's fixed 2 rounds)
	for depth := 1; depth < cfg.MaxDepth; depth++ {
		roundResp, roundCalls, _, err := cfg.Client.CompleteWithTools(cfg.SystemPrompt, followUp, scopedTools)
		if err != nil {
			break
		}
		if len(roundCalls) == 0 {
			// Model is done — save final synthesis response
			if roundResp != "" {
				allResults = append(allResults, StepResult{
					StepID: fmt.Sprintf("synth_%d", depth), Tool: "_response", Result: roundResp,
				})
			}
			break
		}
		// More tool calls — execute them
		followUp = append(followUp, brain.Message{Role: "assistant", Content: roundResp, ToolCalls: roundCalls})
		for _, call := range roundCalls {
			if vErr := ValidateToolCall(call, scopedTools); vErr != nil {
				followUp = append(followUp, brain.Message{
					Role: "tool", Content: vErr.String(), ToolCallID: call.ID,
				})
				continue
			}
			result := executeWithTimeout(cfg, call)
			toolsUsed = append(toolsUsed, call.Function.Name)
			allResults = append(allResults, StepResult{
				StepID: call.ID, Tool: call.Function.Name, Result: result,
			})
			followUp = append(followUp, brain.Message{
				Role: "tool", Content: result, ToolCallID: call.ID,
			})
		}
	}

	return allResults, toolsUsed
}

func executeWithTimeout(cfg PipelineConfig, call brain.ToolCall) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resultCh := make(chan string, 1)
	go func() {
		resultCh <- cfg.ExecuteTool(cfg.Slug, cfg.ChannelID, "", call)
	}()

	select {
	case result := <-resultCh:
		return result
	case <-ctx.Done():
		return fmt.Sprintf(`{"error": "tool '%s' timed out after 30 seconds"}`, call.Function.Name)
	}
}
