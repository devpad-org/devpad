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

func TestToDomainThinkingCopiesEffort(t *testing.T) {
	enabled := true
	thinking := ToDomainThinking(&ThinkingDTO{
		Enabled: &enabled,
		Effort:  "high",
	})

	if thinking == nil {
		t.Fatal("expected thinking config")
	}
	if thinking.Enabled == nil || !*thinking.Enabled {
		t.Fatalf("expected enabled true, got %+v", thinking.Enabled)
	}
	if thinking.Effort != "high" {
		t.Fatalf("expected effort high, got %q", thinking.Effort)
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
		ToolResult:     &domain.ToolResultPart{ToolCallID: "call-1", Name: "list_files", Content: "ok"},
		Approval:       &domain.ApprovalRequest{ID: "approval-1", Command: "sudo ls"},
		ApprovalResult: &domain.ApprovalResult{ID: "approval-1", Command: "sudo ls", Status: "approved"},
		Question: &domain.UserQuestionRequest{
			ID:    "question-1",
			Title: "Choose stack",
			Questions: []domain.UserQuestion{{
				ID:          "stack",
				Prompt:      "Which stack?",
				Type:        domain.UserQuestionSingleChoice,
				Options:     []domain.UserQuestionOption{{Value: "go", Label: "Go"}},
				AllowCustom: true,
			}},
		},
		QuestionResult: &domain.UserQuestionResult{
			ID:      "question-1",
			Status:  "answered",
			Answers: []domain.UserQuestionAnswer{{QuestionID: "stack", Values: []string{"go"}}},
		},
		Plan: []domain.PlanStep{{Title: "Inspect repo", Status: "in_progress"}},
		ContextSize: &domain.ContextSize{
			Approximate:                true,
			ProviderID:                 "openai",
			Model:                      "gpt-5.5",
			NextRequestTokens:          18400,
			TotalTranscriptTokens:      18400,
			ProviderFacingTokens:       18400,
			InputBudgetTokens:          25000,
			PercentageUsed:             73.6,
			WarningThresholdPercentage: 80,
			Warning:                    false,
		},
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
	if dto.ApprovalResolved == nil || dto.ApprovalResolved.Status != "approved" {
		t.Fatalf("unexpected approval result: %+v", dto.ApprovalResolved)
	}
	if dto.QuestionRequired == nil || dto.QuestionRequired.ID != "question-1" || len(dto.QuestionRequired.Questions) != 1 {
		t.Fatalf("unexpected question: %+v", dto.QuestionRequired)
	}
	if dto.QuestionResolved == nil || dto.QuestionResolved.Status != "answered" || len(dto.QuestionResolved.Answers) != 1 {
		t.Fatalf("unexpected question result: %+v", dto.QuestionResolved)
	}
	if len(dto.Plan) != 1 || dto.Plan[0].Title != "Inspect repo" {
		t.Fatalf("unexpected plan: %+v", dto.Plan)
	}
	if dto.ContextSize == nil || dto.ContextSize.NextRequestTokens != 18400 || dto.ContextSize.ProviderID != "openai" {
		t.Fatalf("unexpected context telemetry: %+v", dto.ContextSize)
	}
}

func TestFromAgentRunIncludesInputTurns(t *testing.T) {
	run := &domain.AgentRun{
		ID:             42,
		UserID:         7,
		WorkspaceID:    9,
		ConversationID: 11,
		Model:          "gpt-5.4",
		Status:         domain.AgentRunCompleted,
		InputTurns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "build the feature"),
		},
	}

	dto := FromAgentRun(run)

	if dto.ID != 42 || dto.ConversationID != 11 {
		t.Fatalf("unexpected run metadata: %+v", dto)
	}
	if dto.PromptPreview != "build the feature" {
		t.Fatalf("expected prompt preview, got %q", dto.PromptPreview)
	}
	if len(dto.InputTurns) != 1 {
		t.Fatalf("expected one input turn, got %d", len(dto.InputTurns))
	}
	if dto.InputTurns[0].Role != "user" || len(dto.InputTurns[0].Parts) != 1 || dto.InputTurns[0].Parts[0].Text != "build the feature" {
		t.Fatalf("unexpected input turn: %+v", dto.InputTurns[0])
	}
}

func TestFromAgentRunSummaryOmitsInputTurns(t *testing.T) {
	run := &domain.AgentRun{
		ID:          42,
		UserID:      7,
		WorkspaceID: 9,
		Model:       "gpt-5.4",
		Status:      domain.AgentRunCompleted,
		InputTurns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "build the feature"),
		},
	}

	dto := FromAgentRunSummary(run)

	if len(dto.InputTurns) != 0 {
		t.Fatalf("expected summary to omit input turns, got %+v", dto.InputTurns)
	}
	if dto.PromptPreview != "build the feature" {
		t.Fatalf("expected summary prompt preview, got %q", dto.PromptPreview)
	}
}

func TestAgentRunPromptPreview(t *testing.T) {
	longPrompt := "Please implement this very important feature with enough detail to exceed the preview limit so the run list remains compact and readable for users."
	tests := []struct {
		name  string
		turns []domain.Turn
		want  string
	}{
		{
			name: "uses last non-empty user text",
			turns: []domain.Turn{
				domain.NewTextTurn(domain.RoleAssistant, "previous assistant output"),
				domain.NewTextTurn(domain.RoleUser, "  build\n\tthe   feature  "),
				domain.NewTextTurn(domain.RoleUser, "second user prompt"),
			},
			want: "second user prompt",
		},
		{
			name: "skips user turns without text",
			turns: []domain.Turn{
				domain.NewTextTurn(domain.RoleUser, "earlier prompt"),
				domain.NewToolResultTurn("call-1", "tool", "result only", false),
				domain.NewTextTurn(domain.RoleUser, "next real prompt"),
				domain.NewToolResultTurn("call-2", "tool", "latest result only", false),
			},
			want: "next real prompt",
		},
		{
			name:  "truncates long prompt",
			turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, longPrompt)},
			want:  "Please implement this very important feature with enough detail to exceed the preview limit so the run list remains com…",
		},
		{
			name:  "returns empty without user prompt",
			turns: []domain.Turn{domain.NewTextTurn(domain.RoleAssistant, "assistant only")},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentRunPromptPreview(tt.turns)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
