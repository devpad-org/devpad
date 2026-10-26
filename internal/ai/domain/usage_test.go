package domain

import (
	"encoding/json"
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

func TestModelPricingJSON(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  string
	}{
		{
			name: "long-context prices",
			model: Model{ID: "m", Pricing: &Pricing{
				TokenPrices:          TokenPrices{Input: 2.5, Output: 15, CacheRead: 0.25},
				LongContextThreshold: 272_000,
				LongContext:          &TokenPrices{Input: 5, Output: 22.5, CacheRead: 0.5},
			}},
			want: `{"input":2.5,"output":15,"cacheRead":0.25,"cacheWrite":0,"longContextThreshold":272000,"longContext":{"input":5,"output":22.5,"cacheRead":0.5,"cacheWrite":0}}`,
		},
		{
			name:  "standard prices only",
			model: Model{ID: "m", Pricing: &Pricing{TokenPrices: TokenPrices{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}}},
			want:  `{"input":1,"output":5,"cacheRead":0.1,"cacheWrite":1.25}`,
		},
		{
			name:  "unknown prices",
			model: Model{ID: "m"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.model)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var got struct {
				Pricing json.RawMessage `json:"pricing"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if string(got.Pricing) != tt.want {
				t.Fatalf("pricing = %s, want %s", got.Pricing, tt.want)
			}
		})
	}
}
