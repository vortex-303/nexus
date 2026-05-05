package brain3

// modelRate is the per-million-tokens cost in USD for input and output.
type modelRate struct {
	InputPerM  float64
	OutputPerM float64
}

// modelRates lists the per-model rates we support in v3 (the four allowed
// in resolveModel + Fast variants if/when we add them). Cents-precision is
// fine — the trace's CostUSD is informational, not billing-grade.
//
// Source: platform.claude.com/docs/en/about-claude/models/overview
// (cached 2026-04-15; confirm if Anthropic re-prices and we want
// billing-grade accuracy).
var modelRates = map[string]modelRate{
	"claude-opus-4-7":   {InputPerM: 5.00, OutputPerM: 25.00},
	"claude-opus-4-6":   {InputPerM: 5.00, OutputPerM: 25.00},
	"claude-sonnet-4-6": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-haiku-4-5":  {InputPerM: 1.00, OutputPerM: 5.00},
}

// EstimateCost computes cost in USD for a v3 turn given the model name and
// token counts. Returns 0 for unknown models — better to undercount than
// to invent a rate.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	r, ok := modelRates[model]
	if !ok {
		return 0
	}
	return (float64(inputTokens)*r.InputPerM + float64(outputTokens)*r.OutputPerM) / 1_000_000.0
}
