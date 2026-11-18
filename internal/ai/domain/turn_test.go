package domain

import (
	"encoding/json"
	"testing"
)

func TestWithoutReasoningState(t *testing.T) {
	state := json.RawMessage(`[{"type":"reasoning","encrypted_content":"enc"}]`)
	turns := []Turn{
		NewTextTurn(RoleUser, "build it"),
		{Role: RoleAssistant, Parts: []Part{
			{Kind: PartThinking, Thinking: &ThinkingPart{Text: "first the tests", State: state}},
			{Kind: PartThinking, Thinking: &ThinkingPart{State: state}},
			{Kind: PartText, Text: "On it."},
			{Kind: PartToolCall, ToolCall: &ToolCall{ID: "call_1", ItemID: "fc_1", Type: "function"}},
		}},
	}

	got := WithoutReasoningState(turns)

	assistant := got[1]
	if len(assistant.Parts) != 3 {
		t.Fatalf("expected the thinking part with no text to go, got %+v", assistant.Parts)
	}
	if assistant.ThinkingState() != nil {
		t.Fatalf("expected no reasoning state, got %s", assistant.ThinkingState())
	}
	if assistant.ThinkingText() != "first the tests" {
		t.Fatalf("expected the reasoning's text to stay, got %q", assistant.ThinkingText())
	}
	if assistant.Text() != "On it." {
		t.Fatalf("expected the text to stay, got %q", assistant.Text())
	}
	call := assistant.ToolCalls()[0]
	if call.ID != "call_1" || call.ItemID != "" {
		t.Fatalf("expected the call's ID without its item ID, got %+v", call)
	}
	if got[0].Text() != "build it" {
		t.Fatalf("expected the user's turn to stay, got %q", got[0].Text())
	}

	if turns[1].ThinkingState() == nil || turns[1].ToolCalls()[0].ItemID != "fc_1" || len(turns[1].Parts) != 4 {
		t.Fatalf("expected the original turns untouched, got %+v", turns[1].Parts)
	}
}
