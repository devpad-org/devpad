package context

import "strings"

const (
	defaultInputBudgetTokens   = 25000
	warningThresholdPercentage = 80

	anthropicLongInputBudgetTokens  = 1000000
	anthropicHaikuInputBudgetTokens = 200000
	openAIReasoningInputTokens      = 1050000
	long256KInputBudgetTokens       = 256000
	mistralLarge4InputBudgetTokens  = 1000000
	minimaxM3InputBudgetTokens      = 1000000
	moonshotK3InputBudgetTokens     = 1000000
)

// Budget describes the current approximate provider-facing input budget used
// for context pressure telemetry. Enforcement is added by later phases.
type Budget struct {
	ProviderID                 string
	ModelID                    string
	InputTokens                int
	WarningThresholdPercentage int
}

// BudgetFor returns a conservative input-token budget for display telemetry.
func BudgetFor(providerID, modelID string) Budget {
	provider := strings.ToLower(strings.TrimSpace(providerID))
	model := strings.ToLower(strings.TrimSpace(modelID))

	inputTokens := defaultInputBudgetTokens
	if modelBudget, ok := knownModelInputBudget(model); ok {
		inputTokens = modelBudget
	} else {
		switch {
		case provider == "anthropic" || strings.Contains(model, "claude"):
			inputTokens = anthropicInputBudget(model)
		case provider == "openai" || strings.HasPrefix(model, "gpt-"):
			inputTokens = openAIInputBudget(model)
		case provider == "mistral" || strings.Contains(model, "mistral"):
			inputTokens = mistralInputBudget(model)
		case provider == "moonshot" || strings.Contains(model, "kimi"):
			inputTokens = moonshotInputBudget(model)
		case provider == "minimax" || strings.Contains(model, "minimax"):
			inputTokens = minimaxInputBudget(model)
		}
	}

	return Budget{
		ProviderID:                 providerID,
		ModelID:                    modelID,
		InputTokens:                inputTokens,
		WarningThresholdPercentage: warningThresholdPercentage,
	}
}

func PercentageUsed(tokens, budget int) float64 {
	if tokens <= 0 || budget <= 0 {
		return 0
	}
	return float64(tokens) / float64(budget) * 100
}

func anthropicInputBudget(model string) int {
	if strings.Contains(model, "haiku") {
		return anthropicHaikuInputBudgetTokens
	}
	if strings.Contains(model, "opus") || strings.Contains(model, "sonnet") {
		return anthropicLongInputBudgetTokens
	}
	return anthropicHaikuInputBudgetTokens
}

func openAIInputBudget(model string) int {
	if strings.Contains(model, "gpt-5") {
		return openAIReasoningInputTokens
	}
	return 128000
}

func mistralInputBudget(model string) int {
	if strings.Contains(model, "mistral-large-4") {
		return mistralLarge4InputBudgetTokens
	}
	if strings.Contains(model, "mistral-small-4") ||
		strings.Contains(model, "mistral-small-2603") ||
		strings.Contains(model, "mistral-medium-3") ||
		strings.Contains(model, "mistral-large") {
		return long256KInputBudgetTokens
	}
	return 128000
}

func moonshotInputBudget(model string) int {
	if strings.Contains(model, "k3") {
		return moonshotK3InputBudgetTokens
	}
	return long256KInputBudgetTokens
}

func minimaxInputBudget(model string) int {
	if strings.Contains(model, "m3") {
		return minimaxM3InputBudgetTokens
	}
	return 128000
}

func knownModelInputBudget(model string) (int, bool) {
	switch model {
	case "claude-opus-5-5",
		"claude-sonnet-5-5",
		"claude-haiku-5-5":
		return anthropicLongInputBudgetTokens, true
	case "gpt-5.4",
		"gpt-5.5":
		return openAIReasoningInputTokens, true
	case "mistral-small-latest",
		"mistral-small-2603",
		"mistral-medium-3-5",
		"mistral-medium-3",
		"mistral-large-2512":
		return long256KInputBudgetTokens, true
	case "mistral-large-4",
		"mistral-large-4-0":
		return mistralLarge4InputBudgetTokens, true
	case "kimi-k3":
		return moonshotK3InputBudgetTokens, true
	case "minimax-m3",
		"minimax-m3.1-flash":
		return minimaxM3InputBudgetTokens, true
	default:
		return 0, false
	}
}
