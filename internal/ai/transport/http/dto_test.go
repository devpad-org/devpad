package httptransport

import (
	"encoding/json"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestToDomainTurns(t *testing.T) {
	turns := ToDomainTurns([]MessageDTO{
		{
			Role:             "assistant",
			Content:          "hello",
			ReasoningContent: "thinking",
			ThinkingState:    json.RawMessage(`{"id":"r1"}`),
			ToolCalls: []ToolCallDTO{{
				ID:   "call-1",
				Type: "function",
				Function: ToolCallFunctionDTO{
					Name:      "list_files",
					Arguments: `{"path":"."}`,
				},
			}},
		},
		{
			Role:       "tool",
			Content:    "done",
			ToolCallID: "call-1",
		},
	})

	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}

	assistant := turns[0]
	if assistant.Role != domain.RoleAssistant {
		t.Fatalf("expected assistant role, got %q", assistant.Role)
	}
	if assistant.Text() != "hello" {
		t.Fatalf("expected text hello, got %q", assistant.Text())
	}
	if assistant.ReasoningText() != "thinking" {
		t.Fatalf("expected reasoning thinking, got %q", assistant.ReasoningText())
	}
	if string(assistant.ReasoningState()) != `{"id":"r1"}` {
		t.Fatalf("unexpected reasoning state: %s", assistant.ReasoningState())
	}
	toolCalls := assistant.ToolCalls()
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "list_files" {
		t.Fatalf("unexpected tool calls: %+v", toolCalls)
	}

	toolTurn := turns[1]
	if toolTurn.Role != domain.RoleTool {
		t.Fatalf("expected tool role, got %q", toolTurn.Role)
	}
	if result := toolTurn.ToolResult(); result == nil || result.ToolCallID != "call-1" || result.Content != "done" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestFromDomainTurns(t *testing.T) {
	turns := []domain.Turn{
		{
			Role: domain.RoleAssistant,
			Parts: []domain.Part{
				{Kind: domain.PartText, Text: "hello"},
				{Kind: domain.PartReasoning, Text: "think", ProviderState: json.RawMessage(`{"id":"s1"}`)},
				{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
					ID:   "call-1",
					Type: "function",
					Function: domain.ToolCallFunction{
						Name:      "run_command",
						Arguments: `{"command":"pwd"}`,
					},
				}},
			},
		},
		{
			Role: domain.RoleTool,
			Parts: []domain.Part{{
				Kind: domain.PartToolResult,
				ToolResult: &domain.ToolResultPart{
					ToolCallID: "call-1",
					Content:    "ok",
				},
			}},
		},
	}

	messages := FromDomainTurns(turns)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != "assistant" || messages[0].Content != "hello" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
	if messages[0].ReasoningContent != "think" {
		t.Fatalf("expected reasoning text, got %+v", messages[0])
	}
	if string(messages[0].ThinkingState) != `{"id":"s1"}` {
		t.Fatalf("unexpected thinking state: %s", messages[0].ThinkingState)
	}
	if len(messages[0].ToolCalls) != 1 || messages[0].ToolCalls[0].Function.Name != "run_command" {
		t.Fatalf("unexpected tool calls: %+v", messages[0].ToolCalls)
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "call-1" || messages[1].Content != "ok" {
		t.Fatalf("unexpected tool message: %+v", messages[1])
	}
}

func TestFromClientEvent(t *testing.T) {
	event := domain.ClientEvent{
		TextDelta:      "hello",
		ReasoningDelta: "thinking",
		ReasoningState: json.RawMessage(`{"trace":1}`),
		ToolCalls: []domain.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: domain.ToolCallFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}},
		ToolResult:   &domain.ToolResultPart{ToolCallID: "call-1", Name: "list_files", Content: "ok"},
		Approval:     &domain.ApprovalRequest{ID: "approval-1", Command: "sudo ls"},
		Plan:         []domain.PlanStep{{Title: "Inspect repo", Status: "in_progress"}},
		Done:         true,
		ErrorMessage: "",
	}

	dto := FromClientEvent(event)
	if dto.Content != "hello" || dto.ReasoningContent != "thinking" || !dto.Done {
		t.Fatalf("unexpected DTO: %+v", dto)
	}
	if string(dto.ThinkingState) != `{"trace":1}` {
		t.Fatalf("unexpected thinking state: %s", dto.ThinkingState)
	}
	if len(dto.ToolCalls) != 1 || dto.ToolCalls[0].Function.Name != "list_files" {
		t.Fatalf("unexpected tool calls: %+v", dto.ToolCalls)
	}
	if dto.ToolResult == nil || dto.ToolResult.ToolCallID != "call-1" {
		t.Fatalf("unexpected tool result: %+v", dto.ToolResult)
	}
	if dto.ApprovalRequired == nil || dto.ApprovalRequired.ID != "approval-1" {
		t.Fatalf("unexpected approval: %+v", dto.ApprovalRequired)
	}
	if len(dto.Plan) != 1 || dto.Plan[0].Title != "Inspect repo" {
		t.Fatalf("unexpected plan: %+v", dto.Plan)
	}
}
