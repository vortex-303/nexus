package brain2

import (
	"encoding/json"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"
)

// RunPlanner uses a fast/cheap model to decide which tools to call and in what order.
// Returns a Plan with steps and scoped tool list. Falls back to "use all tools" on error.
func RunPlanner(cfg PipelineConfig) Plan {
	// Build a lightweight tool catalog (names + descriptions only, no full schemas)
	var catalog strings.Builder
	catalog.WriteString("Available tools:\n")
	for _, t := range cfg.AllTools {
		catalog.WriteString("- ")
		catalog.WriteString(t.Function.Name)
		catalog.WriteString(": ")
		catalog.WriteString(t.Function.Description)
		catalog.WriteString("\n")
	}

	plannerPrompt := `You are a task planner. Given a user message and available tools, output a JSON plan.

Rules:
- Each step has: id (s1, s2...), tool (tool name), args (JSON object), depends_on (array of step IDs)
- Steps with no dependencies can run in parallel
- If the user's question needs no tools (general knowledge, opinion, explanation), set direct_answer: true
- Include a "tools" array listing only the tool names needed
- Keep plans minimal — use the fewest tools necessary
- Output ONLY valid JSON, no markdown or explanation

` + catalog.String() + `
Respond with JSON only:`

	// Use last 5 messages for context (planner doesn't need full history)
	plannerMessages := cfg.Messages
	if len(plannerMessages) > 5 {
		plannerMessages = plannerMessages[len(plannerMessages)-5:]
	}

	response, _, err := cfg.PlannerClient.Complete(plannerPrompt, plannerMessages)
	if err != nil {
		return fallbackPlan(cfg)
	}

	// Parse JSON from response (strip markdown fences if present)
	jsonStr := strings.TrimSpace(response)
	if strings.HasPrefix(jsonStr, "```") {
		lines := strings.Split(jsonStr, "\n")
		var inner []string
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				continue
			}
			inner = append(inner, l)
		}
		jsonStr = strings.Join(inner, "\n")
	}

	var plan Plan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return fallbackPlan(cfg)
	}

	// Validate tool names exist
	toolSet := make(map[string]bool, len(cfg.AllTools))
	for _, t := range cfg.AllTools {
		toolSet[t.Function.Name] = true
	}
	for i := len(plan.Steps) - 1; i >= 0; i-- {
		if !toolSet[plan.Steps[i].Tool] {
			// Remove invalid steps
			plan.Steps = append(plan.Steps[:i], plan.Steps[i+1:]...)
		}
	}

	if len(plan.Steps) == 0 && !plan.DirectAnswer {
		plan.DirectAnswer = true
	}

	return plan
}

// fallbackPlan returns a plan that lets the model call tools directly (v1 behavior).
func fallbackPlan(cfg PipelineConfig) Plan {
	toolNames := make([]string, len(cfg.AllTools))
	for i, t := range cfg.AllTools {
		toolNames[i] = t.Function.Name
	}
	return Plan{
		DirectAnswer: false,
		ScopedTools:  toolNames,
	}
}

// ScopeTools filters the full tool catalog to only the tools listed in the plan.
func ScopeTools(allTools []brain.ToolDef, scopedNames []string) []brain.ToolDef {
	if len(scopedNames) == 0 {
		return allTools
	}
	nameSet := make(map[string]bool, len(scopedNames))
	for _, n := range scopedNames {
		nameSet[n] = true
	}
	var scoped []brain.ToolDef
	for _, t := range allTools {
		if nameSet[t.Function.Name] {
			scoped = append(scoped, t)
		}
	}
	if len(scoped) == 0 {
		return allTools // safety: don't send empty tools
	}
	return scoped
}
