package brain2

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
)

// ExecutorOutput is the rich return shape from RunExecutor /
// executeSelfCorrectingLoop. The pipeline used to extract these from a
// 3-tuple but added fields (CostUSD, Items, BudgetExceeded) made that
// awkward; this struct keeps the surface tidy.
type ExecutorOutput struct {
	Results        []StepResult
	ToolsUsed      []string
	LastErr        string
	CostUSD        float64
	Items          []brain.StreamItem
	BudgetExceeded bool // true if MaxCostUSD aborted the loop early
	InputTokens    int  // summed real prompt tokens across all LLM calls
	OutputTokens   int  // summed real completion tokens across all LLM calls
}

// RunExecutor executes the planned steps with parallel execution, validation,
// and a self-correction loop (up to MaxDepth iterations).
func RunExecutor(cfg PipelineConfig, plan Plan) ExecutorOutput {
	if len(plan.Steps) > 0 {
		results, used := executePlan(cfg, plan)
		return ExecutorOutput{Results: results, ToolsUsed: used}
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
// validation, timeouts, multi-round self-correction, loop-detection, and a
// cumulative-cost stop condition driven by cfg.MaxCostUSD.
//
// Returns ExecutorOutput.LastErr when both the tool-call path AND the
// plain-text fallback failed (typical: bad API key, no credits, upstream
// 5xx). Empty LastErr means the model produced usable output.
func executeSelfCorrectingLoop(cfg PipelineConfig, plan Plan) ExecutorOutput {
	scopedTools := ScopeTools(cfg.AllTools, plan.ScopedTools)

	out := ExecutorOutput{}
	addUsage := func(u *brain.CompletionUsage) {
		if u != nil {
			out.CostUSD += u.Cost
			out.InputTokens += u.PromptTokens
			out.OutputTokens += u.CompletionTokens
		}
	}
	overBudget := func() bool {
		return cfg.MaxCostUSD > 0 && out.CostUSD >= cfg.MaxCostUSD
	}

	// Round 1: CompleteWithTools (same as v1)
	llmStart := time.Now()
	responseContent, toolCalls, usage, err := cfg.Client.CompleteWithTools(cfg.SystemPrompt, cfg.Messages, scopedTools)
	addUsage(usage)
	cfg.Trace.AddLLMCall(cfg.Model, time.Since(llmStart), errString(err))
	if err != nil {
		fmt.Printf("[brain2] executor CompleteWithTools error: %v\n", err)
		// Fallback: plain completion
		llmStart = time.Now()
		plainResp, plainUsage, plainErr := cfg.Client.Complete(cfg.SystemPrompt, cfg.Messages)
		addUsage(plainUsage)
		cfg.Trace.AddLLMCall(cfg.Model, time.Since(llmStart), errString(plainErr))
		fmt.Printf("[brain2] fallback Complete: err=%v len=%d\n", plainErr, len(plainResp))
		if plainErr == nil && plainResp != "" {
			out.Results = append(out.Results, StepResult{
				StepID: "fallback_0", Tool: "_response", Result: plainResp,
			})
			out.Items = append(out.Items, brain.StreamItem{Kind: brain.ItemKindText, Text: plainResp})
			return out
		}
		// Both calls failed — surface whichever error has a message; prefer
		// the plain-completion one because that's the most recent attempt.
		errMsg := err.Error()
		if plainErr != nil {
			errMsg = plainErr.Error()
		}
		out.LastErr = errMsg
		return out
	}

	// No tool calls — model answered directly (this is fine, same as v1)
	if len(toolCalls) == 0 {
		if responseContent != "" {
			out.Results = append(out.Results, StepResult{
				StepID: "direct_0", Tool: "_response", Result: responseContent,
			})
			out.Items = append(out.Items, brain.StreamItem{Kind: brain.ItemKindText, Text: responseContent})
		}
		return out
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
			cfg.Trace.AddValidationError(call.Function.Name, vErr.Error)
			followUp = append(followUp, brain.Message{
				Role: "tool", Content: vErr.String(), ToolCallID: call.ID,
			})
			continue
		}

		toolStart := time.Now()
		result := executeWithTimeout(cfg, call)
		cfg.Trace.AddToolCall(call.Function.Name, call.Function.Arguments, result, "", time.Since(toolStart))
		out.ToolsUsed = append(out.ToolsUsed, call.Function.Name)
		out.Results = append(out.Results, StepResult{
			StepID: call.ID, Tool: call.Function.Name, Result: result,
		})
		out.Items = append(out.Items, brain.StreamItem{
			Kind: brain.ItemKindToolCall, Tool: call.Function.Name,
			Args: call.Function.Arguments, CallID: call.ID,
		}, brain.StreamItem{
			Kind: brain.ItemKindToolResult, Tool: call.Function.Name,
			Result: result, CallID: call.ID,
		})
		followUp = append(followUp, brain.Message{
			Role: "tool", Content: result, ToolCallID: call.ID,
		})
	}

	// v1's ResultAsAnswer short-circuit: exactly one tool call whose def is
	// flagged ResultAsAnswer (web_search, generate_image, fetch_url,
	// create_document, search_x) — the raw tool result IS the reply. Skips
	// the synthesizer round entirely: saves an LLM round-trip and stops the
	// synthesizer from paraphrasing away image markdown and source links.
	// len(out.Results)==1 guarantees the call passed validation and executed.
	if len(toolCalls) == 1 && len(out.Results) == 1 {
		if td := findToolDef(scopedTools, toolCalls[0].Function.Name); td != nil && td.Function.ResultAsAnswer {
			out.Results = append(out.Results, StepResult{
				StepID: "raa_0", Tool: "_response", Result: out.Results[0].Result,
			})
			out.Items = append(out.Items, brain.StreamItem{Kind: brain.ItemKindText, Text: out.Results[0].Result})
			return out
		}
	}

	// Loop-detection sliding window. DeepSeek V3/V4's #1 failure mode is
	// over-retry on tool errors — upstream evals show 7-8 rounds where
	// Qwen finishes in 2. Hash (tool_name, args) for every tool the model
	// decides to call and break out early if the same signature appears
	// twice within the last loopWindow attempts. Seeded from round 1 so
	// round-1 → round-2 repeats are caught immediately.
	const loopWindow = 4
	recentSigs := make([]string, 0, 8)
	seenRecently := func(sig string) bool {
		for _, s := range recentSigs {
			if s == sig {
				return true
			}
		}
		return false
	}
	rememberSig := func(sig string) {
		recentSigs = append(recentSigs, sig)
		if len(recentSigs) > loopWindow {
			recentSigs = recentSigs[len(recentSigs)-loopWindow:]
		}
	}
	for _, c := range toolCalls {
		rememberSig(c.Function.Name + "::" + c.Function.Arguments)
	}

	// Round 2+: self-correction loop (v2 improvement over v1's fixed 2 rounds)
	for depth := 1; depth < cfg.MaxDepth; depth++ {
		// Cost-cap stop condition (Codebuff/OpenRouter Agent SDK pattern):
		// abort before the next completion if cumulative spend has crossed
		// MaxCostUSD. Prevents a runaway loop on a model whose token cost
		// jumped (e.g. someone switched from V4 Flash to Opus mid-day).
		if overBudget() {
			fmt.Printf("[brain2] cost-cap reached: cost=%.4f cap=%.4f depth=%d\n", out.CostUSD, cfg.MaxCostUSD, depth)
			out.BudgetExceeded = true
			out.Results = append(out.Results, StepResult{
				StepID: fmt.Sprintf("budget_break_%d", depth),
				Tool:   "_response",
				Result: fmt.Sprintf("I've hit this turn's spend cap (~$%.2f). Let me know if you want me to keep going — an admin can raise the cap in **Settings → Brain → Console** or simplify the request.", cfg.MaxCostUSD),
			})
			break
		}

		// Compress old tool results to save context (keep last 6 full)
		compressedFollowUp := CompressOldToolResults(followUp, 6)

		llmStart = time.Now()
		roundResp, roundCalls, roundUsage, err := cfg.Client.CompleteWithTools(cfg.SystemPrompt, compressedFollowUp, scopedTools)
		addUsage(roundUsage)
		cfg.Trace.AddLLMCall(cfg.Model, time.Since(llmStart), errString(err))
		if err != nil {
			break
		}
		if len(roundCalls) == 0 {
			// Model is done — save final synthesis response
			if roundResp != "" {
				out.Results = append(out.Results, StepResult{
					StepID: fmt.Sprintf("synth_%d", depth), Tool: "_response", Result: roundResp,
				})
				out.Items = append(out.Items, brain.StreamItem{Kind: brain.ItemKindText, Text: roundResp})
			}
			break
		}
		// Detect call-loop BEFORE executing — if the model wants to repeat
		// a call we already ran in the last loopWindow attempts, force-close
		// the turn instead of running it again.
		looped := false
		for _, call := range roundCalls {
			sig := call.Function.Name + "::" + call.Function.Arguments
			if seenRecently(sig) {
				looped = true
				break
			}
		}
		if looped {
			fmt.Printf("[brain2] loop-detection: model is repeating a tool call within %d-call window; force-closing depth=%d\n", loopWindow, depth)
			out.Results = append(out.Results, StepResult{
				StepID: fmt.Sprintf("loop_break_%d", depth),
				Tool:   "_response",
				Result: "I tried that approach a couple of times without progress. Could you give me more detail on what you'd like me to do, or try rephrasing?",
			})
			break
		}
		// More tool calls — execute them
		followUp = append(followUp, brain.Message{Role: "assistant", Content: roundResp, ToolCalls: roundCalls})
		for _, call := range roundCalls {
			if vErr := ValidateToolCall(call, scopedTools); vErr != nil {
				cfg.Trace.AddValidationError(call.Function.Name, vErr.Error)
				followUp = append(followUp, brain.Message{
					Role: "tool", Content: vErr.String(), ToolCallID: call.ID,
				})
				continue
			}
			toolStart := time.Now()
			result := executeWithTimeout(cfg, call)
			cfg.Trace.AddToolCall(call.Function.Name, call.Function.Arguments, result, "", time.Since(toolStart))
			out.ToolsUsed = append(out.ToolsUsed, call.Function.Name)
			out.Results = append(out.Results, StepResult{
				StepID: call.ID, Tool: call.Function.Name, Result: result,
			})
			out.Items = append(out.Items, brain.StreamItem{
				Kind: brain.ItemKindToolCall, Tool: call.Function.Name,
				Args: call.Function.Arguments, CallID: call.ID,
			}, brain.StreamItem{
				Kind: brain.ItemKindToolResult, Tool: call.Function.Name,
				Result: result, CallID: call.ID,
			})
			rememberSig(call.Function.Name + "::" + call.Function.Arguments)

			// Inject budget pressure warning if running low
			budgetWarning := InjectBudgetWarning(depth, cfg.MaxDepth)

			followUp = append(followUp, brain.Message{
				Role: "tool", Content: result + budgetWarning, ToolCallID: call.ID,
			})
		}
	}

	return out
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

// findToolDef returns the tool definition matching name, or nil.
func findToolDef(tools []brain.ToolDef, name string) *brain.ToolDef {
	for i := range tools {
		if tools[i].Function.Name == name {
			return &tools[i]
		}
	}
	return nil
}
