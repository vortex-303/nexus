package brain2

import "strings"

// MaxTokensForModel returns the appropriate max output tokens for a given model.
// Falls back to 2048 for unknown models (same as v1).
func MaxTokensForModel(model string) int {
	lower := strings.ToLower(model)

	// Claude models — large output
	if strings.Contains(lower, "claude") {
		if strings.Contains(lower, "haiku") {
			return 4096
		}
		return 8192
	}

	// GPT-4 variants
	if strings.Contains(lower, "gpt-4o") || strings.Contains(lower, "gpt-4-turbo") {
		return 4096
	}
	if strings.Contains(lower, "gpt-4") {
		return 4096
	}

	// Gemini
	if strings.Contains(lower, "gemini-2") || strings.Contains(lower, "gemini-1.5-pro") {
		return 8192
	}
	if strings.Contains(lower, "gemini") {
		return 4096
	}

	// Grok
	if strings.Contains(lower, "grok") {
		return 4096
	}

	// Large open models
	if strings.Contains(lower, "llama-3") && (strings.Contains(lower, "70b") || strings.Contains(lower, "405b")) {
		return 4096
	}
	if strings.Contains(lower, "mixtral") || strings.Contains(lower, "qwen") {
		return 4096
	}

	// Hermes models (local) — conservative but usable
	if strings.Contains(lower, "hermes") {
		return 4096
	}

	// Default (small models, unknown)
	return 2048
}

// IsHermesModel returns true if the model name indicates a Hermes variant.
func IsHermesModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "hermes")
}

// InferenceParams returns model-optimized inference parameters.
type InferenceParams struct {
	Temperature      float64
	RepetitionPenalty float64
	MaxTokens        int
}

// ParamsForModel returns optimized inference parameters for a given model.
func ParamsForModel(model string) InferenceParams {
	if IsHermesModel(model) {
		return InferenceParams{
			Temperature:      0.8,
			RepetitionPenalty: 1.1,
			MaxTokens:        MaxTokensForModel(model),
		}
	}
	return InferenceParams{
		Temperature:      0.7,
		RepetitionPenalty: 1.0,
		MaxTokens:        MaxTokensForModel(model),
	}
}
