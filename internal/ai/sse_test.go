package ai

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestReadSSEStream_ContentOnly(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan StreamEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Content != "Hello" {
		t.Errorf("first content: got %q, want %q", events[0].Content, "Hello")
	}
	if events[1].Content != " world" {
		t.Errorf("second content: got %q, want %q", events[1].Content, " world")
	}
	if !events[2].Done {
		t.Error("expected final Done event")
	}
}

func TestReadSSEStream_ToolCallAccumulation(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"test.ts\\\",\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"old_text\\\":\\\"hello\\\",\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"new_text\\\":\\\"world\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	ch := make(chan StreamEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	// Should have: ToolCalls event, Done event
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

	// Parse accumulated arguments
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

	ch := make(chan StreamEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
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
	// Generate old_text and new_text that are each ~40KB to test the scanner buffer
	largeText := strings.Repeat("x", 40000)
	argsJSON := fmt.Sprintf(`{"path":"big.txt","old_text":"%s","new_text":"%s"}`, largeText, largeText)

	// Escape the args for inclusion in the SSE JSON (double-encode)
	// The arguments value is a JSON string inside the outer JSON
	escapedArgs := strings.ReplaceAll(argsJSON, `"`, `\"`)
	sse := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_big\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"%s\"}}]}}]}\n\ndata: [DONE]\n\n", escapedArgs)

	ch := make(chan StreamEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	// With the buffer fix, this should parse correctly
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (ToolCalls + Done), got %d", len(events))
	}

	// Check no error was emitted
	for _, e := range events {
		if e.Error != "" {
			t.Fatalf("unexpected error event: %s", e.Error)
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
	// Stream ends without [DONE] — should still emit accumulated tool calls
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"{\\\"path\\\":\\\"f.txt\\\",\\\"old_text\\\":\\\"a\\\",\\\"new_text\\\":\\\"b\\\"}\"}}]}}]}\n\n"

	ch := make(chan StreamEvent, 64)
	go readSSEStream(io.NopCloser(strings.NewReader(sse)), ch)

	var events []StreamEvent
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
