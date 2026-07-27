package brain2

// SkillTrigger describes why an interaction qualifies for autonomous skill creation.
type SkillTrigger struct {
	ToolCallCount     int  // number of tools called
	ErrorRecovery     bool // had validation errors followed by success
	MultipleLLMRounds int  // number of LLM rounds in self-correction loop
}

// ShouldCreateSkill evaluates a trace and its steps to determine if the interaction
// was complex enough to warrant autonomous skill creation.
// Returns nil if no skill should be created.
func ShouldCreateSkill(toolCalls, llmCalls int, success bool, steps []TraceStep) *SkillTrigger {
	if !success {
		return nil
	}

	trigger := &SkillTrigger{
		ToolCallCount:     toolCalls,
		MultipleLLMRounds: llmCalls,
	}

	// Check for error recovery: validation errors that were followed by successful tool calls
	hadValidationError := false
	hadSuccessAfterError := false
	for _, step := range steps {
		if step.StepType == "validation_error" {
			hadValidationError = true
		}
		if hadValidationError && step.StepType == "tool_call" && step.Error == "" {
			hadSuccessAfterError = true
		}
	}
	trigger.ErrorRecovery = hadSuccessAfterError

	// Trigger if: 5+ tool calls, OR error recovery, OR 3+ LLM rounds
	if toolCalls >= 5 || hadSuccessAfterError || llmCalls >= 3 {
		return trigger
	}

	return nil
}
