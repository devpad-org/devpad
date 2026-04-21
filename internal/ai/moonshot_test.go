package ai

import "testing"

func TestBuildMoonshotChatRequest_PreservesReasoningContentWhenThinkingEnabled(t *testing.T) {
	model := NewMoonshotProvider().Models()[0]
	req := ChatRequest{
		Model:    "kimi-k2.6",
		Thinking: &ThinkingConfig{Enabled: boolPtr(true)},
		Messages: []Message{
			{Role: "user", Content: "Inspect the repo"},
			{
				Role:             "assistant",
				ReasoningContent: "Need to inspect README before editing.",
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

	body := buildMoonshotChatRequest(req, model)

	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if body.Thinking == nil {
		t.Fatal("expected thinking configuration")
	}
	if body.Thinking.Type != "enabled" {
		t.Fatalf("expected thinking enabled, got %q", body.Thinking.Type)
	}
	if body.Thinking.Keep != "all" {
		t.Fatalf("expected keep=all, got %q", body.Thinking.Keep)
	}
	if body.Messages[0].ReasoningContent != nil {
		t.Fatal("expected user message to omit reasoning_content")
	}
	if body.Messages[1].ReasoningContent == nil {
		t.Fatal("expected assistant tool-call message to include reasoning_content")
	}
	if *body.Messages[1].ReasoningContent != "Need to inspect README before editing." {
		t.Fatalf("expected preserved reasoning_content, got %q", *body.Messages[1].ReasoningContent)
	}
	if body.Messages[2].ReasoningContent != nil {
		t.Fatal("expected tool result message to omit reasoning_content")
	}
}

func TestBuildMoonshotChatRequest_DisablesThinkingWhenRequested(t *testing.T) {
	model := NewMoonshotProvider().Models()[0]
	req := ChatRequest{
		Model:    "kimi-k2.6",
		Thinking: &ThinkingConfig{Enabled: boolPtr(false)},
		Messages: []Message{{Role: "user", Content: "Hello"}},
	}

	body := buildMoonshotChatRequest(req, model)

	if body.Thinking == nil {
		t.Fatal("expected thinking configuration")
	}
	if body.Thinking.Type != "disabled" {
		t.Fatalf("expected thinking disabled, got %q", body.Thinking.Type)
	}
	if body.Thinking.Keep != "" {
		t.Fatalf("expected keep to be omitted when thinking is disabled, got %q", body.Thinking.Keep)
	}
}
