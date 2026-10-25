package minimax

import (
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func readSSEEvents(sse string) []domain.ProviderEvent {
	ch := make(chan domain.ProviderEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func TestBuildChatRequest_AsksForStreamUsage(t *testing.T) {
	model, _ := domain.ModelByID(NewAdapter().Models(), "MiniMax-M3")
	body := buildChatRequest(aiprovider.StreamRequest{
		Model: "MiniMax-M3",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	}, model)

	if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
		t.Fatalf("expected stream_options.include_usage, got %+v", body.StreamOptions)
	}
}

func TestReadSSEStream_ReportsUsageWithCachedPromptSplitOut(t *testing.T) {
	events := readSSEEvents("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"message\":{\"content\":\"Hi\",\"role\":\"assistant\"}}],\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":300,\"total_tokens\":1500,\"prompt_tokens_details\":{\"cached_tokens\":800}}}\n\n" +
		"data: [DONE]\n\n")

	if len(events) != 3 || events[0].TextDelta != "Hi" || events[1].Usage == nil || !events[2].Done {
		t.Fatalf("expected text, usage, then done, got %+v", events)
	}
	want := domain.Usage{InputTokens: 400, OutputTokens: 300, CacheReadTokens: 800}
	if *events[1].Usage != want {
		t.Fatalf("usage = %+v, want %+v", *events[1].Usage, want)
	}
}

func TestReadSSEStream_ReportsUsageFromChunkWithoutChoices(t *testing.T) {
	events := readSSEEvents("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":7}}\n\n")

	if len(events) != 2 || events[0].Usage == nil || !events[1].Done {
		t.Fatalf("expected usage then done, got %+v", events)
	}
	want := domain.Usage{InputTokens: 50, OutputTokens: 7}
	if *events[0].Usage != want {
		t.Fatalf("usage = %+v, want %+v", *events[0].Usage, want)
	}
}

func TestAdapterModelsArePriced(t *testing.T) {
	for _, model := range NewAdapter().Models() {
		if model.Pricing == nil || model.Pricing.Input <= 0 || model.Pricing.Output <= 0 {
			t.Fatalf("model %s has no prices", model.ID)
		}
	}

	m3, _ := domain.ModelByID(NewAdapter().Models(), "MiniMax-M3")
	long := domain.Usage{InputTokens: 600_000, OutputTokens: 1_000_000}
	if got := m3.Pricing.Cost(long); got != 0.36+2.40 {
		t.Fatalf("long-context cost = %v, want %v", got, 0.36+2.40)
	}
}
