package shared

import (
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func readSSEUsage(t *testing.T, sse string) (domain.Usage, []domain.ProviderEvent) {
	t.Helper()

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	var usages []domain.Usage
	for event := range ch {
		events = append(events, event)
		if event.Usage != nil {
			usages = append(usages, *event.Usage)
		}
	}
	if len(usages) != 1 {
		t.Fatalf("expected one usage event, got %+v", events)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected the stream to end with done, got %+v", events[len(events)-1])
	}
	return usages[0], events
}

func TestReadSSEStream_ReportsUsageFromFinalChoiceChunk(t *testing.T) {
	usage, _ := readSSEUsage(t, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n"+
		"data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":300,\"total_tokens\":1500,\"prompt_tokens_details\":{\"cached_tokens\":800}}}\n\n"+
		"data: [DONE]\n\n")

	want := domain.Usage{InputTokens: 400, OutputTokens: 300, CacheReadTokens: 800}
	if usage != want {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

func TestReadSSEStream_ReportsUsageFromChunkWithoutChoices(t *testing.T) {
	usage, _ := readSSEUsage(t, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n"+
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":50,\"cached_tokens\":600,\"prompt_tokens_details\":{\"cached_tokens\":600,\"cache_write_tokens\":300}}}\n\n"+
		"data: [DONE]\n\n")

	want := domain.Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 600, CacheWriteTokens: 300}
	if usage != want {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

func TestReadSSEStream_ReportsTopLevelCachedTokens(t *testing.T) {
	usage, _ := readSSEUsage(t, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":500,\"completion_tokens\":20,\"cached_tokens\":200}}\n\n")

	want := domain.Usage{InputTokens: 300, OutputTokens: 20, CacheReadTokens: 200}
	if usage != want {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}
