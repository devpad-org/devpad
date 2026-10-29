package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func streamAnthropicResponse(t *testing.T, response string) []domain.ProviderEvent {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	return collectEvents(stream)
}

func usageEvents(events []domain.ProviderEvent) []domain.Usage {
	var usages []domain.Usage
	for _, event := range events {
		if event.Usage != nil {
			usages = append(usages, *event.Usage)
		}
	}
	return usages
}

func TestAnthropicAdapter_StreamReportsUsageBeforeDone(t *testing.T) {
	events := streamAnthropicResponse(t, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":120,"cache_creation_input_tokens":30,"cache_read_input_tokens":2000,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}
`)

	usages := usageEvents(events)
	if len(usages) != 1 {
		t.Fatalf("expected one usage event, got %d: %+v", len(usages), events)
	}
	want := domain.Usage{InputTokens: 120, OutputTokens: 42, CacheReadTokens: 2000, CacheWriteTokens: 30}
	if usages[0] != want {
		t.Fatalf("usage = %+v, want %+v", usages[0], want)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected the stream to end with done, got %+v", events[len(events)-1])
	}
}

func TestAnthropicAdapter_StreamReportsUsageBeforeRefusal(t *testing.T) {
	events := streamAnthropicResponse(t, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":80,"output_tokens":1}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"bio"}},"usage":{"output_tokens":7}}
`)

	var usageAt, errAt = -1, -1
	for i, event := range events {
		if event.Usage != nil {
			usageAt = i
		}
		if event.Err != nil {
			errAt = i
		}
	}
	if usageAt < 0 || errAt < 0 || usageAt > errAt {
		t.Fatalf("expected usage before the refusal error, got %+v", events)
	}
	want := domain.Usage{InputTokens: 80, OutputTokens: 7}
	if *events[usageAt].Usage != want {
		t.Fatalf("usage = %+v, want %+v", *events[usageAt].Usage, want)
	}
}

func TestAnthropicAdapter_StreamWithoutUsageReportsNone(t *testing.T) {
	events := streamAnthropicResponse(t, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5"}}

event: message_stop
data: {"type":"message_stop"}
`)

	if usages := usageEvents(events); len(usages) != 0 {
		t.Fatalf("expected no usage events, got %+v", usages)
	}
}

func TestAnthropicAdapter_ModelsArePriced(t *testing.T) {
	for _, model := range NewAdapter().Models() {
		if model.Pricing == nil || model.Pricing.Input <= 0 || model.Pricing.Output <= 0 {
			t.Errorf("model %s has no prices", model.ID)
		}
	}
}

func TestAnthropicAdapter_StreamReportsEachFallbackAttempt(t *testing.T) {
	// The requested model declines before any output and a fallback model
	// answers; message_start already names the fallback model.
	events := streamAnthropicResponse(t, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-4-8","usage":{"input_tokens":412,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"fallback","from":{"model":"claude-opus-5-5"},"to":{"model":"claude-opus-4-8"},"trigger":{"type":"refusal","category":"cyber"}}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":264,"iterations":[{"type":"message","model":"claude-opus-5-5","input_tokens":535,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},{"type":"fallback_message","model":"claude-opus-4-8","input_tokens":412,"output_tokens":264,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}]}}

event: message_stop
data: {"type":"message_stop"}

`)
	usages := usageEvents(events)
	want := []domain.Usage{
		{Model: "claude-opus-5-5", InputTokens: 535},
		{Model: "claude-opus-4-8", InputTokens: 412, OutputTokens: 264, CacheReadTokens: 100},
	}
	if len(usages) != len(want) {
		t.Fatalf("usages = %+v, want %+v", usages, want)
	}
	for i := range want {
		if usages[i] != want[i] {
			t.Fatalf("usage %d = %+v, want %+v", i, usages[i], want[i])
		}
	}
}

func TestAnthropicAdapter_FallbackModelsArePriced(t *testing.T) {
	model, ok := domain.ModelByID(NewAdapter().Models(), "claude-opus-5-5")
	if !ok {
		t.Fatal("expected claude-opus-5-5 in the catalog")
	}
	for _, id := range []string{"claude-opus-5", "claude-opus-4-8", "claude-sonnet-5-5"} {
		if _, ok := model.FallbackPricing[id]; !ok {
			t.Errorf("expected the prices of %s, a model a request may fall back to", id)
		}
	}
	haiku, _ := domain.ModelByID(NewAdapter().Models(), "claude-haiku-4-5")
	if haiku.FallbackPricing != nil {
		t.Errorf("expected no fallback prices on a model without fallbacks")
	}
}
