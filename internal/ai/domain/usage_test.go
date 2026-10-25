package domain

import (
	"math"
	"testing"
)

func TestPricingCost(t *testing.T) {
	pricing := Pricing{
		TokenPrices:          TokenPrices{Input: 2, Output: 10, CacheRead: 0.5, CacheWrite: 2.5},
		LongContextThreshold: 1000,
		LongContext:          &TokenPrices{Input: 4, Output: 20, CacheRead: 1, CacheWrite: 5},
	}

	tests := []struct {
		name  string
		usage Usage
		want  float64
	}{
		{
			name:  "every kind of token at the standard prices",
			usage: Usage{InputTokens: 400, OutputTokens: 100, CacheReadTokens: 200, CacheWriteTokens: 400},
			want:  (400*2 + 100*10 + 200*0.5 + 400*2.5) / 1e6,
		},
		{
			name:  "a prompt at the threshold stays standard",
			usage: Usage{InputTokens: 600, CacheReadTokens: 400, OutputTokens: 10},
			want:  (600*2 + 400*0.5 + 10*10) / 1e6,
		},
		{
			name:  "cached tokens count toward a long prompt",
			usage: Usage{InputTokens: 600, CacheReadTokens: 401, OutputTokens: 10},
			want:  (600*4 + 401*1 + 10*20) / 1e6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pricing.Cost(tt.usage); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("Cost() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPricingCostWithoutLongContextPrices(t *testing.T) {
	pricing := Pricing{TokenPrices: TokenPrices{Input: 1, Output: 5}}
	got := pricing.Cost(Usage{InputTokens: 5_000_000, OutputTokens: 1_000_000})
	if math.Abs(got-10) > 1e-9 {
		t.Fatalf("Cost() = %v, want 10", got)
	}
}
