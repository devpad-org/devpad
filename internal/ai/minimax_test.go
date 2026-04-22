package ai

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestBuildMiniMaxChatRequest_UsesReasoningSplitAndReasoningDetails(t *testing.T) {
	thinkingState := json.RawMessage(`[
		{
			"type":"reasoning.text",
			"id":"reasoning-text-9",
			"format":"MiniMax-response-v1",
			"index":0,
			"text":"I should inspect the current files before editing."
		}
	]`)

	model := NewMiniMaxProvider().Models()[0]
	req := ChatRequest{
		Model: "MiniMax-M2.7",
		Messages: []Message{
			{Role: "user", Content: "Check the repo."},
			{
				Role:             "assistant",
				Content:          "",
				ReasoningContent: "I should inspect the current files before editing.",
				ThinkingState:    thinkingState,
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "README contents"},
		},
		Tools: []ToolDefinition{{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read a file",
			},
		}},
	}

	body := buildMiniMaxChatRequest(req, model)

	if !body.ReasoningSplit {
		t.Fatal("expected reasoning_split to be enabled for MiniMax")
	}
	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if len(body.Messages[0].ReasoningDetails) != 0 {
		t.Fatal("expected user message to omit reasoning_details")
	}
	if len(body.Messages[1].ReasoningDetails) != 1 {
		t.Fatalf("expected assistant message to include one reasoning detail, got %d", len(body.Messages[1].ReasoningDetails))
	}
	detail := body.Messages[1].ReasoningDetails[0]
	if detail.Type != "reasoning.text" {
		t.Fatalf("expected reasoning detail type reasoning.text, got %q", detail.Type)
	}
	if detail.ID != "reasoning-text-9" {
		t.Fatalf("expected preserved reasoning detail id, got %q", detail.ID)
	}
	if detail.Format != "MiniMax-response-v1" {
		t.Fatalf("expected MiniMax reasoning format, got %q", detail.Format)
	}
	if detail.Text != "I should inspect the current files before editing." {
		t.Fatalf("expected preserved reasoning text, got %q", detail.Text)
	}
	if body.Messages[1].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("expected tool call name read_file, got %q", body.Messages[1].ToolCalls[0].Function.Name)
	}
}

func TestReadMiniMaxSSEStream_ReasoningSplitUsesCumulativeDeltas(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"id\":\"reasoning-text-1\",\"format\":\"MiniMax-response-v1\",\"index\":0,\"text\":\"Plan\"}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\",\"id\":\"reasoning-text-1\",\"format\":\"MiniMax-response-v1\",\"index\":0,\"text\":\"Plan more\"}],\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan StreamEvent, 64)
	go readMiniMaxSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
	for event := range ch {
		events = append(events, event)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if events[0].ReasoningContent != "Plan" {
		t.Fatalf("expected first reasoning chunk Plan, got %q", events[0].ReasoningContent)
	}
	if string(events[0].ThinkingState) != `[{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":0,"text":"Plan"}]` {
		t.Fatalf("expected first thinking state payload to be preserved, got %s", string(events[0].ThinkingState))
	}
	if events[1].ReasoningContent != " more" {
		t.Fatalf("expected second reasoning delta ' more', got %q", events[1].ReasoningContent)
	}
	if events[2].Content != "Hello" {
		t.Fatalf("expected first content delta Hello, got %q", events[2].Content)
	}
	if events[3].Content != " world" {
		t.Fatalf("expected second content delta ' world', got %q", events[3].Content)
	}
	if !events[4].Done {
		t.Fatal("expected final Done event")
	}
}

func TestReadMiniMaxSSEStream_ToolCallsStillAccumulate(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan StreamEvent, 64)
	go readMiniMaxSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
	for event := range ch {
		events = append(events, event)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}
	if events[0].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("expected accumulated tool arguments, got %q", events[0].ToolCalls[0].Function.Arguments)
	}
	if !events[1].Done {
		t.Fatal("expected final Done event")
	}
}
