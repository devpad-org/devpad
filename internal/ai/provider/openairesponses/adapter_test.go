package openairesponses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func TestAdapterMetadata(t *testing.T) {
	adapter := NewAdapter()

	if adapter.ProviderID() != "openai" {
		t.Fatalf("expected provider ID openai, got %q", adapter.ProviderID())
	}
	if adapter.ProviderName() != "OpenAI" {
		t.Fatalf("expected provider name OpenAI, got %q", adapter.ProviderName())
	}
	if adapter.Protocol() != aiprovider.ProtocolOpenAIResponses {
		t.Fatalf("expected protocol openai_responses, got %q", adapter.Protocol())
	}

	models := adapter.Models()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	expected := []struct {
		id               string
		enabledByDefault bool
	}{
		{id: "gpt-5.4", enabledByDefault: false},
		{id: "gpt-5.5", enabledByDefault: true},
	}
	for index, expectedModel := range expected {
		if models[index].ID != expectedModel.id {
			t.Fatalf("expected model %d to be %q, got %q", index, expectedModel.id, models[index].ID)
		}
		if models[index].ProviderID != "openai" {
			t.Fatalf("expected model %q provider to be openai, got %q", models[index].ID, models[index].ProviderID)
		}
		if !models[index].Thinking.Supported {
			t.Fatalf("expected model %q to support thinking", models[index].ID)
		}
		if models[index].Thinking.EnabledByDefault != expectedModel.enabledByDefault {
			t.Fatalf("expected model %q thinking enabled by default to be %v, got %v", models[index].ID, expectedModel.enabledByDefault, models[index].Thinking.EnabledByDefault)
		}
		if !models[index].Thinking.CanDisable {
			t.Fatalf("expected model %q thinking to be disableable", models[index].ID)
		}
	}
}

func TestBuildResponsesRequest_SystemAndToolMapping(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "gpt-5.4",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "You are concise."),
			domain.NewTextTurn(domain.RoleSystem, "Use tools when needed."),
			domain.NewTextTurn(domain.RoleUser, "hello"),
		},
		Tools: []domain.ToolDefinition{{
			Type: "function",
			Function: domain.ToolFunction{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
			},
		}},
	}

	body := buildResponsesRequest(req, NewAdapter().Models()[0])

	if body["instructions"] != "You are concise.\n\nUse tools when needed." {
		t.Fatalf("unexpected instructions: %v", body["instructions"])
	}
	if body["model"] != "gpt-5.4" {
		t.Fatalf("unexpected model: %v", body["model"])
	}
	if body["stream"] != true {
		t.Fatalf("expected streaming request")
	}
	if body["store"] != false {
		t.Fatalf("expected store=false")
	}

	input := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	first := input[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hello" {
		t.Fatalf("unexpected input item: %+v", first)
	}

	tools := body["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0]["type"] != "function" {
		t.Fatalf("expected function tool, got %v", tools[0]["type"])
	}
	if tools[0]["name"] != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %v", tools[0]["name"])
	}
	if tools[0]["strict"] != false {
		t.Fatalf("expected strict=false, got %v", tools[0]["strict"])
	}

	if _, ok := body["include"]; ok {
		t.Fatal("expected include to be omitted when thinking is not enabled")
	}

	if _, ok := body["reasoning"]; ok {
		t.Fatal("expected reasoning config to be omitted when thinking is not enabled")
	}
}

func TestBuildResponsesRequest_AssistantToolHistoryAndReasoningState(t *testing.T) {
	reasoningState := json.RawMessage(`[{"id":"rs_123","type":"reasoning","summary":[],"encrypted_content":"enc"}]`)
	req := aiprovider.StreamRequest{
		Model: "gpt-5.4",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "Check the weather."),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{Kind: domain.PartText, Text: "I'll look that up."},
					{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{State: reasoningState}},
					{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
						ID:   "call_123",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"Paris"}`,
						},
					}},
				},
			},
			domain.NewToolResultTurn("call_123", "get_weather", "15C", false),
		},
	}

	input := buildResponsesRequest(req, NewAdapter().Models()[0])["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("expected 5 input items, got %d", len(input))
	}

	if input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("expected first input item to be the user message, got %+v", input[0])
	}

	reasoningItem := input[1].(map[string]any)
	if reasoningItem["type"] != "reasoning" {
		t.Fatalf("expected preserved reasoning item, got %+v", reasoningItem)
	}

	assistantItem := input[2].(map[string]any)
	if assistantItem["role"] != "assistant" || assistantItem["content"] != "I'll look that up." {
		t.Fatalf("unexpected assistant item: %+v", assistantItem)
	}

	toolCallItem := input[3].(map[string]any)
	if toolCallItem["type"] != "function_call" || toolCallItem["call_id"] != "call_123" {
		t.Fatalf("unexpected function_call item: %+v", toolCallItem)
	}

	toolResultItem := input[4].(map[string]any)
	if toolResultItem["type"] != "function_call_output" || toolResultItem["call_id"] != "call_123" {
		t.Fatalf("unexpected function_call_output item: %+v", toolResultItem)
	}
	if toolResultItem["output"] != "15C" {
		t.Fatalf("unexpected tool output: %+v", toolResultItem)
	}
}

func TestBuildResponsesRequest_ThinkingEnabled(t *testing.T) {
	enabled := true
	body := buildResponsesRequest(aiprovider.StreamRequest{
		Model:    "gpt-5.5",
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		Thinking: &domain.ThinkingConfig{Enabled: &enabled},
	}, NewAdapter().Models()[1])

	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != defaultReasoningEffort {
		t.Fatalf("expected reasoning effort %q, got %v", defaultReasoningEffort, reasoning["effort"])
	}
	if reasoning["summary"] != "auto" {
		t.Fatalf("expected reasoning summary auto, got %v", reasoning["summary"])
	}

	include := body["include"].([]string)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected include reasoning.encrypted_content when thinking is enabled, got %v", include)
	}
}

func TestBuildResponsesRequest_ThinkingDisabled(t *testing.T) {
	disabled := false
	body := buildResponsesRequest(aiprovider.StreamRequest{
		Model:    "gpt-5.5",
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		Thinking: &domain.ThinkingConfig{Enabled: &disabled},
	}, NewAdapter().Models()[1])

	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != disabledReasoningEffort {
		t.Fatalf("expected reasoning effort %q, got %v", disabledReasoningEffort, reasoning["effort"])
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("expected reasoning summary to be omitted when thinking is disabled, got %v", reasoning["summary"])
	}
	if _, ok := body["include"]; ok {
		t.Fatal("expected include to be omitted when thinking is disabled")
	}
}

func TestAdapterStreamTextAndDone(t *testing.T) {
	var authHeader string
	var acceptHeader string
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		acceptHeader = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_123\",\"output_index\":0,\"content_index\":0,\"delta\":\"Hello\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_123\",\"output_index\":0,\"content_index\":0,\"delta\":\" world\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
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

	events := collectEvents(stream)
	if authHeader != "Bearer test-key" {
		t.Fatalf("expected bearer auth header, got %q", authHeader)
	}
	if acceptHeader != "text/event-stream" {
		t.Fatalf("expected SSE accept header, got %q", acceptHeader)
	}
	if requestBody["model"] != "gpt-5.5" {
		t.Fatalf("expected model gpt-5.5 in request, got %v", requestBody["model"])
	}
	if len(events) < 3 {
		t.Fatalf("expected content/content/done events, got %d", len(events))
	}
	if events[0].TextDelta != "Hello" {
		t.Fatalf("expected first content Hello, got %q", events[0].TextDelta)
	}
	if events[1].TextDelta != " world" {
		t.Fatalf("expected second content ' world', got %q", events[1].TextDelta)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected final done event, got %+v", events[len(events)-1])
	}
}

func TestAdapterStreamReasoningAndToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"item_id\":\"rs_123\",\"output_index\":0,\"summary_index\":0,\"delta\":\"Plan\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_123\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"enc\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_123\",\"call_id\":\"call_123\",\"name\":\"get_weather\",\"arguments\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_123\",\"output_index\":1,\"delta\":\"{\\\"location\\\":\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_123\",\"output_index\":1,\"delta\":\"\\\"Paris\\\"}\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_123\",\"output_index\":1,\"name\":\"get_weather\",\"arguments\":\"{\\\"location\\\":\\\"Paris\\\"}\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_123\",\"call_id\":\"call_123\",\"name\":\"get_weather\",\"arguments\":\"{\\\"location\\\":\\\"Paris\\\"}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	stream, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "gpt-5.4",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "weather in Paris")},
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	events := collectEvents(stream)
	if len(events) < 4 {
		t.Fatalf("expected reasoning, thinking state, tool calls, done events, got %d", len(events))
	}

	var sawReasoning bool
	var sawThinkingState bool
	var toolCallEvent *domain.ProviderEvent
	for i := range events {
		if events[i].ReasoningDelta == "Plan" {
			sawReasoning = true
		}
		if len(events[i].ReasoningState) > 0 {
			sawThinkingState = true
		}
		if len(events[i].ToolCalls) > 0 {
			toolCallEvent = &events[i]
		}
	}

	if !sawReasoning {
		t.Fatal("expected reasoning delta event")
	}
	if !sawThinkingState {
		t.Fatal("expected thinking state event")
	}
	if toolCallEvent == nil {
		t.Fatal("expected tool call event")
	}
	if len(toolCallEvent.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(toolCallEvent.ToolCalls))
	}
	if toolCallEvent.ToolCalls[0].ID != "call_123" {
		t.Fatalf("expected tool call id call_123, got %q", toolCallEvent.ToolCalls[0].ID)
	}
	if toolCallEvent.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q", toolCallEvent.ToolCalls[0].Function.Name)
	}
	if toolCallEvent.ToolCalls[0].Function.Arguments != `{"location":"Paris"}` {
		t.Fatalf("unexpected arguments: %q", toolCallEvent.ToolCalls[0].Function.Arguments)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected final done event, got %+v", events[len(events)-1])
	}
}

func TestAdapterErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	_, err := adapter.Stream(context.Background(), aiprovider.Credentials{APIKey: "bad-key"}, aiprovider.StreamRequest{
		Model: "gpt-5.4",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err == nil {
		t.Fatal("expected stream error")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Fatalf("expected provider ID in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestAdapterContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	adapter := NewAdapter()
	adapter.baseURL = server.URL
	adapter.client = server.Client()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.Stream(ctx, aiprovider.Credentials{APIKey: "test-key"}, aiprovider.StreamRequest{
		Model: "gpt-5.4",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}

func collectEvents(stream <-chan domain.ProviderEvent) []domain.ProviderEvent {
	var events []domain.ProviderEvent
	for event := range stream {
		events = append(events, event)
	}

	return events
}
