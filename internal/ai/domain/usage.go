package domain

// Usage is the tokens one or more provider requests were billed for and,
// when the model's prices are known, what they cost.
type Usage struct {
	// InputTokens excludes the prompt tokens read from or written to the
	// provider's cache, which are billed at their own rates.
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64 `json:"cacheWriteTokens,omitempty"`
	// CostUSD is nil when the model has no known prices.
	CostUSD *float64 `json:"costUsd,omitempty"`
	// Model is the model that ran the request when the provider ran it on
	// another than the one asked for, such as a fallback model after a
	// refusal. Empty means the one asked for.
	Model string `json:"model,omitempty"`
}

// PromptTokens is the size of the prompt the request sent.
func (u Usage) PromptTokens() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// TokenPrices are USD per million tokens.
type TokenPrices struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Pricing is what a model costs per token. Prices are kept with each
// provider's model list and must be updated by hand when providers change
// them.
type Pricing struct {
	TokenPrices
	// LongContext replaces the standard prices for a request whose prompt is
	// over LongContextThreshold tokens.
	LongContextThreshold int64        `json:"longContextThreshold,omitempty"`
	LongContext          *TokenPrices `json:"longContext,omitempty"`
}

// Cost returns the USD cost of a single request's usage.
func (p Pricing) Cost(u Usage) float64 {
	prices := p.TokenPrices
	if p.LongContext != nil && u.PromptTokens() > p.LongContextThreshold {
		prices = *p.LongContext
	}
	return (float64(u.InputTokens)*prices.Input +
		float64(u.OutputTokens)*prices.Output +
		float64(u.CacheReadTokens)*prices.CacheRead +
		float64(u.CacheWriteTokens)*prices.CacheWrite) / 1_000_000
}
