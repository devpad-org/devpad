package shared

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestReadSSEStream_ContentOnly(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].TextDelta != "Hello" {
		t.Errorf("first content: got %q, want %q", events[0].TextDelta, "Hello")
	}
	if events[1].TextDelta != " world" {
		t.Errorf("second content: got %q, want %q", events[1].TextDelta, " world")
	}
	if !events[2].Done {
		t.Error("expected final Done event")
	}
}

func TestReadSSEStream_ReasoningContent(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Plan: inspect files.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\" Then update code.\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].ReasoningDelta != "Plan: inspect files." {
		t.Fatalf("first reasoning chunk: got %q", events[0].ReasoningDelta)
	}
	if events[1].ReasoningDelta != " Then update code." {
		t.Fatalf("second reasoning chunk: got %q", events[1].ReasoningDelta)
	}
	if !events[2].Done {
		t.Fatal("expected final Done event")
	}
}

func TestReadSSEStream_ContentThinkingChunks(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"I should reason this through."}]},{"type":"text","text":"Final answer."}]}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].ReasoningDelta != "I should reason this through." {
		t.Fatalf("thinking chunk: got %q", events[0].ReasoningDelta)
	}
	if events[1].TextDelta != "Final answer." {
		t.Fatalf("text chunk: got %q", events[1].TextDelta)
	}
	if !events[2].Done {
		t.Fatal("expected final Done event")
	}
}

func TestReadSSEStream_ToolCallAccumulation(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"test.ts\\\",\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"old_text\\\":\\\"hello\\\",\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"new_text\\\":\\\"world\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}

	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}

	tc := events[0].ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tool call ID: got %q, want %q", tc.ID, "call_1")
	}
	if tc.Function.Name != "edit_file" {
		t.Errorf("tool call name: got %q, want %q", tc.Function.Name, "edit_file")
	}

	expectedArgs := `{"path":"test.ts","old_text":"hello","new_text":"world"}`
	if tc.Function.Arguments != expectedArgs {
		t.Errorf("tool call args:\n  got:  %q\n  want: %q", tc.Function.Arguments, expectedArgs)
	}

	if !events[1].Done {
		t.Error("expected final Done event")
	}
}

func TestReadSSEStream_MultipleToolCalls(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"{\\\"path\\\":\\\"b.txt\\\",\\\"old_text\\\":\\\"x\\\",\\\"new_text\\\":\\\"y\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if len(events[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(events[0].ToolCalls))
	}

	if events[0].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("first tool call name: got %q", events[0].ToolCalls[0].Function.Name)
	}
	if events[0].ToolCalls[1].Function.Name != "edit_file" {
		t.Errorf("second tool call name: got %q", events[0].ToolCalls[1].Function.Name)
	}
}

func TestReadSSEStream_LargeToolCallArgs(t *testing.T) {
	largeText := strings.Repeat("x", 40000)
	argsJSON := fmt.Sprintf(`{"path":"big.txt","old_text":"%s","new_text":"%s"}`, largeText, largeText)

	escapedArgs := strings.ReplaceAll(argsJSON, `"`, `\"`)
	sse := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_big\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"%s\"}}]}}]}\n\ndata: [DONE]\n\n", escapedArgs)

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (ToolCalls + Done), got %d", len(events))
	}

	for _, e := range events {
		if e.Err != nil {
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}

	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}

	tc := events[0].ToolCalls[0]
	if tc.Function.Arguments != argsJSON {
		t.Errorf("arguments mismatch (len got=%d, want=%d)", len(tc.Function.Arguments), len(argsJSON))
	}
}

func TestReadSSEStream_NoExplicitDone(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"{\\\"path\\\":\\\"f.txt\\\",\\\"old_text\\\":\\\"a\\\",\\\"new_text\\\":\\\"b\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"

	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events (ToolCalls + Done), got %d", len(events))
	}
	if len(events[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(events[0].ToolCalls))
	}
	if !events[1].Done {
		t.Error("expected Done event")
	}
}

func TestReadSSEStream_ReportsFailures(t *testing.T) {
	toolCallChunk := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"edit_file","arguments":"{\"path\":"}}]}}]}` + "\n\n"

	tests := []struct {
		name    string
		sse     string
		wantErr string
	}{
		{
			name:    "cut off at the output token limit",
			sse:     "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
			wantErr: "Test stopped the response because it reached the output token limit",
		},
		{
			name:    "stopped by the content filter",
			sse:     "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n",
			wantErr: "Test stopped the response with its content filter",
		},
		{
			name:    "error object mid-stream",
			sse:     "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\ndata: {\"error\":{\"message\":\"overloaded\",\"type\":\"server_error\"}}\n\n",
			wantErr: "Test returned an error: overloaded",
		},
		{
			name:    "error string mid-stream",
			sse:     "data: {\"error\":\"rate limited\"}\n\n",
			wantErr: "Test returned an error: rate limited",
		},
		{
			name:    "Mistral-style error object",
			sse:     "data: {\"object\":\"error\",\"message\":\"invalid request\",\"type\":\"invalid_request_error\"}\n\n",
			wantErr: "Test returned an error: invalid request",
		},
		{
			name:    "unreadable event",
			sse:     "data: {\"choices\":[{\"delta\":\n\n",
			wantErr: "Test sent a response event that could not be read",
		},
		{
			name:    "stream closes mid tool call",
			sse:     toolCallChunk,
			wantErr: "Test response stream ended before the response finished",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := collectSSEEvents(tt.sse)
			last := events[len(events)-1]
			if last.Err == nil || last.Err.Error() != tt.wantErr {
				t.Fatalf("expected final error %q, got %+v", tt.wantErr, last)
			}
			for _, event := range events {
				if event.Done {
					t.Fatal("a failed stream must not report Done")
				}
				if len(event.ToolCalls) > 0 {
					t.Fatal("a failed stream must not emit its tool calls")
				}
			}
		})
	}
}

func TestReadSSEStream_UsageOnlyChunkIsNotAnError(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1}}\n\n" +
		"data: [DONE]\n\n"

	events := collectSSEEvents(sse)
	for _, event := range events {
		if event.Err != nil {
			t.Fatalf("unexpected error event: %v", event.Err)
		}
	}
	if !events[len(events)-1].Done {
		t.Fatalf("expected final Done event, got %+v", events[len(events)-1])
	}
}

func collectSSEEvents(sse string) []domain.ProviderEvent {
	ch := make(chan domain.ProviderEvent, 64)
	go ReadSSEStream("Test", io.NopCloser(strings.NewReader(sse)), ch)

	var events []domain.ProviderEvent
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func TestReadSSEStream_ReportsUsageOfATruncatedResponse(t *testing.T) {
	events := collectSSEEvents("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":\"length\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n")

	if len(events) != 3 || events[1].Usage == nil || events[2].Err == nil {
		t.Fatalf("expected text, usage, then an error, got %+v", events)
	}
	if events[1].Usage.OutputTokens != 7 {
		t.Fatalf("expected the truncated response's output tokens, got %+v", *events[1].Usage)
	}
}
