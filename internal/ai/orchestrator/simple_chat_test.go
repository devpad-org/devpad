package orchestrator

import (
	"context"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestSimpleChatOrchestrator_PricesUsage(t *testing.T) {
	orch := NewSimpleChatOrchestrator(&mockChatService{
		findModelFn: func(modelID string) (domain.Model, error) {
			return domain.Model{ID: modelID, Pricing: &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 2, Output: 10}}}, nil
		},
		chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
			ch := make(chan domain.ProviderEvent, 2)
			ch <- domain.ProviderEvent{Usage: &domain.Usage{InputTokens: 1000, OutputTokens: 100}}
			ch <- domain.ProviderEvent{Done: true}
			close(ch)
			return ch, nil
		},
	})
	stream, err := orch.Stream(context.Background(), domain.ChatRequest{Model: "model-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var usage *domain.Usage
	for _, event := range collectEvents(t, stream) {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil || usage.CostUSD == nil || *usage.CostUSD != (1000*2+100*10)/1e6 {
		t.Fatalf("expected the request priced, got %+v", usage)
	}
}
