package openairesponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func streamResponsesEvents(t *testing.T, lines ...string) []domain.ProviderEvent {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			_, _ = w.Write([]byte("data: " + line + "\n\n"))
		}
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "gpt-5.5",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	return collectEvents(stream)
}

func TestAdapterStreamReportsUsageWithCachedInputSplitOut(t *testing.T) {
	events := streamResponsesEvents(t,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":5000,"input_tokens_details":{"cached_tokens":4000},"output_tokens":300,"output_tokens_details":{"reasoning_tokens":200}}}}`,
	)

	var usages []domain.Usage
	for _, event := range events {
		if event.Usage != nil {
			usages = append(usages, *event.Usage)
		}
	}
	if len(usages) != 1 {
		t.Fatalf("expected one usage event, got %+v", events)
	}
	want := domain.Usage{InputTokens: 1000, OutputTokens: 300, CacheReadTokens: 4000}
	if usages[0] != want {
		t.Fatalf("usage = %+v, want %+v", usages[0], want)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected the stream to end with done, got %+v", events[len(events)-1])
	}
}

func TestAdapterStreamReportsUsageBeforeIncompleteError(t *testing.T) {
	events := streamResponsesEvents(t,
		`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":900,"output_tokens":64}}}`,
	)

	if len(events) != 2 || events[0].Usage == nil || events[1].Err == nil {
		t.Fatalf("expected usage then an error, got %+v", events)
	}
	want := domain.Usage{InputTokens: 900, OutputTokens: 64}
	if *events[0].Usage != want {
		t.Fatalf("usage = %+v, want %+v", *events[0].Usage, want)
	}
}

func TestAdapterModelsArePricedWithLongContextRates(t *testing.T) {
	for _, model := range NewAdapter().Models() {
		pricing := model.Pricing
		if pricing == nil || pricing.Input <= 0 || pricing.Output <= 0 {
			t.Fatalf("model %s has no prices", model.ID)
		}
		if pricing.LongContext == nil || pricing.LongContext.Input <= pricing.Input {
			t.Fatalf("model %s has no long-context prices", model.ID)
		}
	}
}
