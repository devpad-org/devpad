package httptransport

import (
	"encoding/json"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func TestToDomainTurns(t *testing.T) {
	turns := ToDomainTurns([]TurnDTO{
		{
			Role: "assistant",
			Parts: []PartDTO{
				{Kind: "text", Text: "hello"},
				{Kind: "thinking", Thinking: &ThinkingPartDTO{
					Text:  "thinking",
					State: json.RawMessage(`{"id":"r1"}`),
				}},
				{Kind: "tool_call", ToolCall: &ToolCallDTO{
					ID:        "call-1",
					Name:      "list_files",
					Arguments: `{"path":"."}`,
				}},
			},
		},
		{
			Role: "user",
			Parts: []PartDTO{
				{Kind: "tool_result", ToolResult: &ToolResultDTO{
					ToolCallID: "call-1",
					Name:       "list_files",
					Content:    "done",
				}},
			},
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
	if assistant.ThinkingText() != "thinking" {
		t.Fatalf("expected thinking text, got %q", assistant.ThinkingText())
	}
	if string(assistant.ThinkingState()) != `{"id":"r1"}` {
		t.Fatalf("unexpected thinking state: %s", assistant.ThinkingState())
	}
	toolCalls := assistant.ToolCalls()
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "list_files" {
		t.Fatalf("unexpected tool calls: %+v", toolCalls)
	}

	userTurn := turns[1]
	if userTurn.Role != domain.RoleUser {
		t.Fatalf("expected user role, got %q", userTurn.Role)
	}
	result := userTurn.ToolResult()
	if result == nil || result.ToolCallID != "call-1" || result.Content != "done" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestFromDomainTurns(t *testing.T) {
	turns := []domain.Turn{
		{
			Role: domain.RoleAssistant,
			Parts: []domain.Part{
				{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{
					Text:  "think",
					State: json.RawMessage(`{"id":"s1"}`),
				}},
				{Kind: domain.PartText, Text: "hello"},
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
			Role: domain.RoleUser,
			Parts: []domain.Part{{
				Kind: domain.PartToolResult,
				ToolResult: &domain.ToolResultPart{
					ToolCallID: "call-1",
					Name:       "run_command",
					Content:    "ok",
				},
			}},
		},
	}

	dtos := FromDomainTurns(turns)
	if len(dtos) != 2 {
		t.Fatalf("expected 2 turn DTOs, got %d", len(dtos))
	}

	assistant := dtos[0]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", assistant.Role)
	}
	if len(assistant.Parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(assistant.Parts))
	}

	thinkingPart := assistant.Parts[0]
	if thinkingPart.Kind != "thinking" || thinkingPart.Thinking == nil {
		t.Fatalf("expected thinking part, got %+v", thinkingPart)
	}
	if thinkingPart.Thinking.Text != "think" {
		t.Fatalf("expected thinking text, got %q", thinkingPart.Thinking.Text)
	}
	if string(thinkingPart.Thinking.State) != `{"id":"s1"}` {
		t.Fatalf("unexpected thinking state: %s", thinkingPart.Thinking.State)
	}

	textPart := assistant.Parts[1]
	if textPart.Kind != "text" || textPart.Text != "hello" {
		t.Fatalf("unexpected text part: %+v", textPart)
	}

	toolCallPart := assistant.Parts[2]
	if toolCallPart.Kind != "tool_call" || toolCallPart.ToolCall == nil {
		t.Fatalf("expected tool_call part, got %+v", toolCallPart)
	}
	if toolCallPart.ToolCall.Name != "run_command" {
		t.Fatalf("expected tool name run_command, got %q", toolCallPart.ToolCall.Name)
	}

	userTurn := dtos[1]
	if userTurn.Role != "user" {
		t.Fatalf("expected user role, got %q", userTurn.Role)
	}
	if len(userTurn.Parts) != 1 || userTurn.Parts[0].Kind != "tool_result" {
		t.Fatalf("expected tool_result part, got %+v", userTurn.Parts)
	}
	tr := userTurn.Parts[0].ToolResult
	if tr == nil || tr.ToolCallID != "call-1" || tr.Content != "ok" {
		t.Fatalf("unexpected tool result: %+v", tr)
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
	if len(dto.ToolCalls) != 1 || dto.ToolCalls[0].Name != "list_files" {
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
