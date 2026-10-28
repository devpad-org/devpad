package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func TestAnthropicAdapter_Metadata(t *testing.T) {
	adapter := NewAdapter()

	if adapter.ProviderID() != "anthropic" {
		t.Fatalf("expected provider ID anthropic, got %q", adapter.ProviderID())
	}
	if adapter.ProviderName() != "Anthropic" {
		t.Fatalf("expected provider name Anthropic, got %q", adapter.ProviderName())
	}
	if adapter.Protocol() != aiprovider.ProtocolAnthropic {
		t.Fatalf("expected protocol anthropic_messages, got %q", adapter.Protocol())
	}

	models := adapter.Models()
	if len(models) != 5 {
		t.Fatalf("expected 5 models, got %d", len(models))
	}

	allEfforts := []string{"low", "medium", "high", "xhigh", "max"}
	expectedModels := []struct {
		id               string
		name             string
		enabledByDefault bool
		canDisable       bool
		efforts          []string
		defaultEffort    string
	}{
		{
			id:               "claude-opus-5-5",
			name:             "Claude Opus 5.5",
			enabledByDefault: true,
			canDisable:       false,
			efforts:          allEfforts,
			defaultEffort:    "medium",
		},
		{
			id:               "claude-sonnet-5-5",
			name:             "Claude Sonnet 5.5",
			enabledByDefault: true,
			canDisable:       true,
			efforts:          allEfforts,
			defaultEffort:    "medium",
		},
		{
			id:            "claude-opus-4-7",
			name:          "Claude Opus 4.7",
			canDisable:    true,
			efforts:       allEfforts,
			defaultEffort: "xhigh",
		},
		{
			id:            "claude-sonnet-4-6",
			name:          "Claude Sonnet 4.6",
			canDisable:    true,
			efforts:       []string{"low", "medium", "high", "max"},
			defaultEffort: "medium",
		},
		{id: "claude-haiku-4-5", name: "Claude Haiku 4.5", canDisable: true},
	}
	for i, expected := range expectedModels {
		if models[i].ID != expected.id {
			t.Fatalf("expected model %d to be %q, got %q", i, expected.id, models[i].ID)
		}
		if models[i].Name != expected.name {
			t.Fatalf("expected model %d name to be %q, got %q", i, expected.name, models[i].Name)
		}
		if models[i].ProviderID != "anthropic" {
			t.Fatalf("expected model %d provider ID to be anthropic, got %q", i, models[i].ProviderID)
		}
		if !models[i].Thinking.Supported {
			t.Fatalf("expected model %d thinking to be supported", i)
		}
		if models[i].Thinking.EnabledByDefault != expected.enabledByDefault {
			t.Fatalf("expected model %d thinking enabledByDefault %v, got %v", i, expected.enabledByDefault, models[i].Thinking.EnabledByDefault)
		}
		if models[i].Thinking.CanDisable != expected.canDisable {
			t.Fatalf("expected model %d thinking canDisable %v, got %v", i, expected.canDisable, models[i].Thinking.CanDisable)
		}
		if !slices.Equal(models[i].Thinking.SupportedEfforts, expected.efforts) {
			t.Fatalf("expected model %d efforts to be %v, got %v", i, expected.efforts, models[i].Thinking.SupportedEfforts)
		}
		if models[i].Thinking.DefaultEffort != expected.defaultEffort {
			t.Fatalf("expected model %d default effort to be %q, got %q", i, expected.defaultEffort, models[i].Thinking.DefaultEffort)
		}
	}
}

func TestAnthropicAdapter_StreamSendsBetasForClaude55(t *testing.T) {
	tests := []struct {
		model    string
		wantBeta string
	}{
		{model: "claude-opus-5-5", wantBeta: "server-side-fallback-2026-07-01,thinking-binding-controls-2026-08-01"},
		{model: "claude-sonnet-5-5", wantBeta: "server-side-fallback-2026-07-01,thinking-binding-controls-2026-08-01"},
		{model: "claude-opus-4-7", wantBeta: ""},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			var betaHeader string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				betaHeader = r.Header.Get("anthropic-beta")
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n"))
			}))
			defer server.Close()

			adapter := NewAdapter()
			adapter.baseURL = server.URL
			adapter.client = server.Client()

			stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
				Model: tt.model,
				Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
			})
			if err != nil {
				t.Fatalf("unexpected stream error: %v", err)
			}
			collectEvents(stream)

			if betaHeader != tt.wantBeta {
				t.Fatalf("expected anthropic-beta %q, got %q", tt.wantBeta, betaHeader)
			}
		})
	}
}

func TestAnthropicAdapter_StreamRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"bio","explanation":null}}}

event: message_stop
data: {"type":"message_stop"}
`
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

	events := collectEvents(stream)
	last := events[len(events)-1]
	if last.Err == nil {
		t.Fatalf("expected refusal error, got %+v", last)
	}
	if !strings.Contains(last.Err.Error(), "declined") || !strings.Contains(last.Err.Error(), "bio") {
		t.Fatalf("expected refusal error naming the category, got %q", last.Err.Error())
	}
}

func TestAnthropicAdapter_StreamFallbackDropsBlocksBeforeBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-declined"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_declined","name":"read_file"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"fallback","from":{"model":"claude-opus-5-5"},"to":{"model":"claude-opus-5"}}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"signature_delta","signature":"sig-fallback"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: content_block_start
data: {"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"toolu_fallback","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":4}

event: message_stop
data: {"type":"message_stop"}
`
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

	var reasoningState string
	var toolCalls []domain.ToolCall
	for _, event := range collectEvents(stream) {
		if event.Err != nil {
			t.Fatalf("unexpected stream event error: %v", event.Err)
		}
		if len(event.ReasoningState) > 0 {
			reasoningState = string(event.ReasoningState)
		}
		if len(event.ToolCalls) > 0 {
			toolCalls = event.ToolCalls
		}
	}

	if strings.Contains(reasoningState, "sig-declined") || !strings.Contains(reasoningState, "sig-fallback") {
		t.Fatalf("expected only the fallback model's thinking block, got %s", reasoningState)
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "toolu_fallback" {
		t.Fatalf("expected only the fallback model's tool call, got %+v", toolCalls)
	}
	if toolCalls[0].Function.Arguments != `{"path":"a.go"}` {
		t.Fatalf("expected fallback tool arguments, got %q", toolCalls[0].Function.Arguments)
	}
}

func TestAnthropicAdapter_StreamTextContent(t *testing.T) {
	var authHeader string
	var versionHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("x-api-key")
		versionHeader = r.Header.Get("anthropic-version")

		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-opus-4-7"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-4-7",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if authHeader != "test-key" {
		t.Fatalf("expected x-api-key header test-key, got %q", authHeader)
	}
	if versionHeader != "2023-06-01" {
		t.Fatalf("expected anthropic-version 2023-06-01, got %q", versionHeader)
	}

	events := collectEvents(stream)
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (2 content + done), got %d", len(events))
	}

	if events[0].TextDelta != "Hello" {
		t.Fatalf("expected first content Hello, got %q", events[0].TextDelta)
	}
	if events[1].TextDelta != " world" {
		t.Fatalf("expected second content ' world', got %q", events[1].TextDelta)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected final done event")
	}
}

func TestAnthropicAdapter_RetriesOverloadedAPI(t *testing.T) {
	var calls int64
	var retryAuth, retryVersion atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
			return
		}
		retryAuth.Store(r.Header.Get("x-api-key"))
		retryVersion.Store(r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_stop
data: {"type":"message_stop"}
`))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-4-7",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("expected the overloaded request to be retried, got %v", err)
	}
	collectEvents(stream)
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if got := retryAuth.Load(); got != "test-key" {
		t.Fatalf("expected x-api-key on the retried attempt, got %v", got)
	}
	if got := retryVersion.Load(); got != "2023-06-01" {
		t.Fatalf("expected anthropic-version on the retried attempt, got %v", got)
	}
}

func TestAnthropicAdapter_StreamThinkingContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_stop
data: {"type":"message_stop"}
`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-sonnet-4-6",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	events := collectEvents(stream)
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(events))
	}

	if events[0].ReasoningDelta != "Let me think" {
		t.Fatalf("expected reasoning content 'Let me think', got %q", events[0].ReasoningDelta)
	}
	if events[1].TextDelta != "Answer" {
		t.Fatalf("expected content 'Answer', got %q", events[1].TextDelta)
	}
}

func TestAnthropicAdapter_StreamToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-opus-4-7"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"NYC\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-4-7",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "what's the weather")},
		Tools: []domain.ToolDefinition{
			{
				Type: "function",
				Function: domain.ToolFunction{
					Name:        "get_weather",
					Description: "Get weather",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	events := collectEvents(stream)
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (tool call + done), got %d", len(events))
	}

	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}

	toolCall := events[0].ToolCalls[0]
	if toolCall.ID != "toolu_123" {
		t.Fatalf("expected tool call ID toolu_123, got %q", toolCall.ID)
	}
	if toolCall.Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q", toolCall.Function.Name)
	}
	if toolCall.Function.Arguments != `{"location":"NYC"}` {
		t.Fatalf("expected arguments {\"location\":\"NYC\"}, got %q", toolCall.Function.Arguments)
	}
}

func TestAnthropicAdapter_StreamMultipleToolUsesAsSingleEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		response := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-opus-4-7"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"b.txt\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_stop
data: {"type":"message_stop"}
`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-4-7",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "read two files")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	events := collectEvents(stream)
	if len(events) < 2 {
		t.Fatalf("expected tool calls and done events, got %d", len(events))
	}
	if len(events[0].ToolCalls) != 2 {
		t.Fatalf("expected both tool calls in one event, got %d", len(events[0].ToolCalls))
	}
	if events[0].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("expected first tool args, got %q", events[0].ToolCalls[0].Function.Arguments)
	}
	if events[0].ToolCalls[1].Function.Arguments != `{"path":"b.txt"}` {
		t.Fatalf("expected second tool args, got %q", events[0].ToolCalls[1].Function.Arguments)
	}
}

func TestAnthropicAdapter_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"Invalid API key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	_, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "bad-key"}, aiprovider.StreamRequest{
		Model: "claude-opus-4-7",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})

	if err == nil {
		t.Fatal("expected error for invalid API key")
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("expected error to mention provider, got %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected error to mention status code, got %v", err)
	}
}

func collectEvents(stream <-chan domain.ProviderEvent) []domain.ProviderEvent {
	var events []domain.ProviderEvent
	for event := range stream {
		events = append(events, event)
	}
	return events
}
