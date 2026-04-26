package httptransport

import (
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ChatRequestDTO is the frontend request contract for chat endpoints.
type ChatRequestDTO struct {
	Model       string       `json:"model"`
	Messages    []MessageDTO `json:"messages"`
	Thinking    *ThinkingDTO `json:"thinking,omitempty"`
	WorkspaceID int64        `json:"workspaceId,omitempty"`
}

// ThinkingDTO controls thinking mode over the transport boundary.
type ThinkingDTO struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// MessageDTO is the stable frontend transport shape for a chat message.
type MessageDTO struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ThinkingState    json.RawMessage `json:"thinking_state,omitempty"`
	ToolCalls        []ToolCallDTO   `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

// StreamEventDTO is the stable SSE payload sent to the frontend.
type StreamEventDTO struct {
	ReasoningContent string              `json:"reasoningContent,omitempty"`
	ThinkingState    json.RawMessage     `json:"thinkingState,omitempty"`
	Content          string              `json:"content,omitempty"`
	ToolCalls        []ToolCallDTO       `json:"toolCalls,omitempty"`
	ToolResult       *ToolResultDTO      `json:"toolResult,omitempty"`
	ApprovalRequired *ApprovalRequestDTO `json:"approvalRequired,omitempty"`
	Plan             []PlanStepDTO       `json:"plan,omitempty"`
	Done             bool                `json:"done,omitempty"`
	Error            string              `json:"error,omitempty"`
}

// ToolCallDTO is the transport representation of a requested tool call.
type ToolCallDTO struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function ToolCallFunctionDTO `json:"function"`
}

// ToolCallFunctionDTO contains the function name and JSON arguments.
type ToolCallFunctionDTO struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResultDTO is sent over SSE after tool execution.
type ToolResultDTO struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Content    string `json:"content"`
}

// ApprovalRequestDTO asks the client to approve a command.
type ApprovalRequestDTO struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// PlanStepDTO is the frontend transport representation of an execution plan step.
type PlanStepDTO struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ToDomainThinking maps a transport thinking config into the domain shape.
func ToDomainThinking(dto *ThinkingDTO) *domain.ThinkingConfig {
	if dto == nil {
		return nil
	}

	return &domain.ThinkingConfig{Enabled: dto.Enabled}
}

// ToDomainTurns converts transport messages into normalized internal turns.
func ToDomainTurns(messages []MessageDTO) []domain.Turn {
	turns := make([]domain.Turn, 0, len(messages))
	for _, message := range messages {
		turn := domain.Turn{Role: domain.Role(message.Role)}

		if message.Role == string(domain.RoleTool) {
			if message.Content != "" || message.ToolCallID != "" {
				turn.Parts = append(turn.Parts, domain.Part{
					Kind: domain.PartToolResult,
					ToolResult: &domain.ToolResultPart{
						ToolCallID: message.ToolCallID,
						Content:    message.Content,
					},
				})
			}
		} else {
			if message.Content != "" {
				turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: message.Content})
			}
			if message.ReasoningContent != "" || len(message.ThinkingState) > 0 {
				turn.Parts = append(turn.Parts, domain.Part{
					Kind:          domain.PartReasoning,
					Text:          message.ReasoningContent,
					ProviderState: domain.CloneRawMessage(message.ThinkingState),
				})
			}
			for _, toolCall := range message.ToolCalls {
				domainToolCall := toDomainToolCall(toolCall)
				turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &domainToolCall})
			}
		}

		turns = append(turns, turn)
	}

	return turns
}

// FromDomainTurns converts normalized turns back into the stable message transport shape.
func FromDomainTurns(turns []domain.Turn) []MessageDTO {
	messages := make([]MessageDTO, 0, len(turns))
	for _, turn := range turns {
		message := MessageDTO{Role: string(turn.Role)}
		if turn.Role == domain.RoleTool {
			if result := turn.ToolResult(); result != nil {
				message.Content = result.Content
				message.ToolCallID = result.ToolCallID
			}
			messages = append(messages, message)
			continue
		}

		message.Content = turn.Text()
		message.ReasoningContent = turn.ReasoningText()
		message.ThinkingState = turn.ReasoningState()

		toolCalls := turn.ToolCalls()
		if len(toolCalls) > 0 {
			message.ToolCalls = fromDomainToolCalls(toolCalls)
		}

		messages = append(messages, message)
	}

	return messages
}

// FromClientEvent converts an internal client event into the stable SSE payload.
func FromClientEvent(event domain.ClientEvent) StreamEventDTO {
	dto := StreamEventDTO{
		ReasoningContent: event.ReasoningDelta,
		ThinkingState:    domain.CloneRawMessage(event.ReasoningState),
		Content:          event.TextDelta,
		Done:             event.Done,
		Error:            event.ErrorMessage,
	}

	if len(event.ToolCalls) > 0 {
		dto.ToolCalls = fromDomainToolCalls(event.ToolCalls)
	}
	if event.ToolResult != nil {
		dto.ToolResult = &ToolResultDTO{
			ToolCallID: event.ToolResult.ToolCallID,
			Name:       event.ToolResult.Name,
			Content:    event.ToolResult.Content,
		}
	}
	if event.Approval != nil {
		dto.ApprovalRequired = &ApprovalRequestDTO{
			ID:      event.Approval.ID,
			Command: event.Approval.Command,
		}
	}
	if len(event.Plan) > 0 {
		dto.Plan = fromDomainPlan(event.Plan)
	}

	return dto
}

func toDomainToolCall(dto ToolCallDTO) domain.ToolCall {
	return domain.ToolCall{
		ID:   dto.ID,
		Type: dto.Type,
		Function: domain.ToolCallFunction{
			Name:      dto.Function.Name,
			Arguments: dto.Function.Arguments,
		},
	}
}

func fromDomainToolCalls(toolCalls []domain.ToolCall) []ToolCallDTO {
	dtos := make([]ToolCallDTO, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		dtos = append(dtos, ToolCallDTO{
			ID:   toolCall.ID,
			Type: toolCall.Type,
			Function: ToolCallFunctionDTO{
				Name:      toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			},
		})
	}

	return dtos
}

func fromDomainPlan(plan []domain.PlanStep) []PlanStepDTO {
	dtos := make([]PlanStepDTO, 0, len(plan))
	for _, step := range plan {
		dtos = append(dtos, PlanStepDTO{Title: step.Title, Status: step.Status})
	}

	return dtos
}
