package brain2

import (
	"github.com/nexus-chat/nexus/internal/brain"
)

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
