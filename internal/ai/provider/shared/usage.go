package shared

import "github.com/devpad-org/devpad/internal/ai/domain"

// ReportedUsage is a request's usage from a provider whose prompt count
// includes the tokens read from and written to its cache.
type ReportedUsage struct {
	PromptTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	OutputTokens     int64
}

// Billed splits the cached and cache-written tokens out of the prompt, since
// each is billed at its own rate. The prompt count is the limit: cache counts
// above it are cut down, cached tokens first, so that no token is billed twice.
func (r ReportedUsage) Billed() *domain.Usage {
	cached := min(r.CachedTokens, r.PromptTokens)
	written := min(r.CacheWriteTokens, r.PromptTokens-cached)
	return &domain.Usage{
		InputTokens:      r.PromptTokens - cached - written,
		OutputTokens:     r.OutputTokens,
		CacheReadTokens:  cached,
		CacheWriteTokens: written,
	}
}
