package shared

import (
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestReportedUsageBilled(t *testing.T) {
	tests := []struct {
		name     string
		reported ReportedUsage
		want     domain.Usage
	}{
		{
			name:     "no cache",
			reported: ReportedUsage{PromptTokens: 500, OutputTokens: 20},
			want:     domain.Usage{InputTokens: 500, OutputTokens: 20},
		},
		{
			name:     "cached and written tokens leave the input",
			reported: ReportedUsage{PromptTokens: 12000, CachedTokens: 4000, CacheWriteTokens: 7000, OutputTokens: 300},
			want:     domain.Usage{InputTokens: 1000, OutputTokens: 300, CacheReadTokens: 4000, CacheWriteTokens: 7000},
		},
		{
			name:     "cached tokens are cut to the prompt",
			reported: ReportedUsage{PromptTokens: 100, CachedTokens: 150, CacheWriteTokens: 10, OutputTokens: 5},
			want:     domain.Usage{OutputTokens: 5, CacheReadTokens: 100},
		},
		{
			name:     "written tokens are cut to what cached tokens leave",
			reported: ReportedUsage{PromptTokens: 100, CachedTokens: 60, CacheWriteTokens: 70, OutputTokens: 5},
			want:     domain.Usage{OutputTokens: 5, CacheReadTokens: 60, CacheWriteTokens: 40},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := *tt.reported.Billed(); got != tt.want {
				t.Fatalf("usage = %+v, want %+v", got, tt.want)
			}
		})
	}
}
