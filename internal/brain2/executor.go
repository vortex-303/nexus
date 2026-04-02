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

// executeSelfCorrectingLoop is the fallback when the planner doesn't produce
// specific steps. It uses the LLM to decide tools iteratively (Hermes-style).
func executeSelfCorrectingLoop(cfg PipelineConfig, plan Plan) ([]StepResult, []string) {
	scopedTools := ScopeTools(cfg.AllTools, plan.ScopedTools)
	messages := make([]brain.Message, len(cfg.Messages))
	copy(messages, cfg.Messages)

	var allResults []StepResult
	var toolsUsed []string

	for depth := 0; depth < cfg.MaxDepth; depth++ {
		responseContent, toolCalls, _, err := cfg.Client.CompleteWithTools(cfg.SystemPrompt, messages, scopedTools)
		if err != nil {
			fmt.Printf("[brain2] executor error at depth %d: %v\n", depth, err)
			// On error, try a plain completion as last resort
			if depth == 0 {
				plainResp, _, plainErr := cfg.Client.Complete(cfg.SystemPrompt, messages)
				fmt.Printf("[brain2] fallback Complete: err=%v len=%d\n", plainErr, len(plainResp))
				if plainErr == nil && plainResp != "" {
					allResults = append(allResults, StepResult{
						StepID: "fallback_0", Tool: "_response", Result: plainResp,
					})
				}
			}
			break
		}

		if len(toolCalls) == 0 {
			// Model is done — response is the final answer (passed to synthesizer)
			if responseContent != "" {
				allResults = append(allResults, StepResult{
					StepID: fmt.Sprintf("final_%d", depth),
					Tool:   "_response",
					Result: responseContent,
				})
			}
			break
		}

		// Execute each tool call
		for _, call := range toolCalls {
			// Validate first
			if vErr := ValidateToolCall(call, scopedTools); vErr != nil {
				// Feed structured error back to model for self-correction
				messages = append(messages, brain.Message{
					Role:       "assistant",
					Content:    responseContent,
					ToolCalls:  []brain.ToolCall{call},
				})
				messages = append(messages, brain.Message{
					Role:       "tool",
					Content:    vErr.String(),
					ToolCallID: call.ID,
				})
				continue
			}

			// Execute with timeout
			result := executeWithTimeout(cfg, call)
			toolsUsed = append(toolsUsed, call.Function.Name)

			allResults = append(allResults, StepResult{
				StepID: call.ID,
				Tool:   call.Function.Name,
				Result: result,
			})

			// Append to conversation for next iteration
			messages = append(messages, brain.Message{
				Role:       "assistant",
				Content:    responseContent,
				ToolCalls:  []brain.ToolCall{call},
			})
			messages = append(messages, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
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
