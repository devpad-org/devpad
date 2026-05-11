package domain

// ContextSize is numeric-only telemetry about the approximate provider-facing
// input size for the next request. It intentionally excludes raw prompt/tool
// content so it is safe to persist and show in the UI.
type ContextSize struct {
	Approximate                bool
	ProviderID                 string
	Model                      string
	NextRequestTokens          int
	TotalTranscriptTokens      int
	ProviderFacingTokens       int
	InputBudgetTokens          int
	PercentageUsed             float64
	WarningThresholdPercentage int
	Warning                    bool
}
