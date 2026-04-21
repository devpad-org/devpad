package ai

import "testing"

func TestBuildMoonshotChatRequest_AddsReasoningContentForAssistantToolCalls(t *testing.T) {
	req := ChatRequest{
		Model: "kimi-k2.6",
		Messages: []Message{
			{Role: "user", Content: "Inspect the repo"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "# Devpad"},
		},
	}

	body := buildMoonshotChatRequest(req)

	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if body.Messages[0].ReasoningContent != nil {
		t.Fatal("expected user message to omit reasoning_content")
	}
	if body.Messages[1].ReasoningContent == nil {
		t.Fatal("expected assistant tool-call message to include reasoning_content")
	}
	if *body.Messages[1].ReasoningContent != "" {
		t.Fatalf("expected empty reasoning_content, got %q", *body.Messages[1].ReasoningContent)
	}
	if body.Messages[2].ReasoningContent != nil {
		t.Fatal("expected tool result message to omit reasoning_content")
	}
}
