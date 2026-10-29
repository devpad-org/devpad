package orchestrator

import (
	"context"
	"testing"
	"time"

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
	stream, err := orch.Stream(context.Background(), domain.ChatRequest{Model: "model-1"}, nil)
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

func TestSimpleChatOrchestrator_RecordsARequestCutShort(t *testing.T) {
	provider := make(chan domain.ProviderEvent, 4)
	orch := NewSimpleChatOrchestrator(&mockChatService{
		chatStreamFn: func(_ context.Context, _ domain.ChatRequest) (<-chan domain.ProviderEvent, error) {
			return provider, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorded := make(chan domain.Usage, 1)
	stream, err := orch.Stream(ctx, domain.ChatRequest{Model: "model-1"}, func(usage domain.Usage) { recorded <- usage })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	provider <- domain.ProviderEvent{TextDelta: "hi"}
	if event := nextEvent(t, stream); event.TextDelta != "hi" {
		t.Fatalf("expected the text, got %+v", event)
	}

	cancel()
	collectEvents(t, stream)
	provider <- domain.ProviderEvent{Usage: &domain.Usage{InputTokens: 10}}
	close(provider)

	select {
	case usage := <-recorded:
		if usage.InputTokens != 10 {
			t.Fatalf("expected the request recorded, got %+v", usage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request cut short was never recorded")
	}
}
