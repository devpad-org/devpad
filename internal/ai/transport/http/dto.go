package httptransport

import (
	"encoding/json"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ChatRequestDTO is the frontend request contract for chat endpoints.
type ChatRequestDTO struct {
	Model       string      `json:"model"`
	Turns       []TurnDTO   `json:"turns"`
	Thinking    *ThinkingDTO `json:"thinking,omitempty"`
	WorkspaceID int64        `json:"workspaceId,omitempty"`
}

// ThinkingDTO controls thinking mode over the transport boundary.
type ThinkingDTO struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// TurnDTO is the stable frontend transport shape for a conversation turn.
type TurnDTO struct {
	Role  string    `json:"role"`
	Parts []PartDTO `json:"parts"`
}

// PartDTO carries one normalized piece of a turn.
type PartDTO struct {
	Kind       string          `json:"kind"`
	Text       string          `json:"text,omitempty"`
	Thinking   *ThinkingPartDTO `json:"thinking,omitempty"`
	ToolCall   *ToolCallDTO     `json:"toolCall,omitempty"`
	ToolResult *ToolResultDTO   `json:"toolResult,omitempty"`
}

// ThinkingPartDTO carries the reasoning text and opaque provider state.
type ThinkingPartDTO struct {
	Text  string          `json:"text,omitempty"`
	State json.RawMessage `json:"state,omitempty"`
}

// ToolCallDTO is the transport representation of a requested tool call.
type ToolCallDTO struct {
	ID        string `json:"id"`
	ItemID    string `json:"itemId,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResultDTO is the transport representation of a tool execution result.
type ToolResultDTO struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"isError,omitempty"`
}

// StreamEventDTO is the stable SSE payload sent to the frontend.
type StreamEventDTO struct {
	ReasoningContent string              `json:"reasoningContent,omitempty"`
	ThinkingState    json.RawMessage     `json:"thinkingState,omitempty"`
	Content          string              `json:"content,omitempty"`
	ToolCalls        []StreamToolCallDTO `json:"toolCalls,omitempty"`
	ToolResult       *StreamToolResultDTO `json:"toolResult,omitempty"`
	ApprovalRequired *ApprovalRequestDTO `json:"approvalRequired,omitempty"`
	Plan             []PlanStepDTO       `json:"plan,omitempty"`
	Done             bool                `json:"done,omitempty"`
	Error            string              `json:"error,omitempty"`
}

// StreamToolCallDTO carries a tool call in a streaming SSE event.
type StreamToolCallDTO struct {
	ID        string `json:"id"`
	ItemID    string `json:"itemId,omitempty"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamToolResultDTO is sent over SSE after tool execution.
type StreamToolResultDTO struct {
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

// ToDomainTurns converts transport turns into normalized internal turns.
func ToDomainTurns(dtos []TurnDTO) []domain.Turn {
	turns := make([]domain.Turn, 0, len(dtos))
	for _, dto := range dtos {
		turn := domain.Turn{Role: domain.Role(dto.Role)}
		for _, p := range dto.Parts {
			part := toDomainPart(p)
			turn.Parts = append(turn.Parts, part)
		}
		turns = append(turns, turn)
	}
	return turns
}

func toDomainPart(dto PartDTO) domain.Part {
	switch domain.PartKind(dto.Kind) {
	case domain.PartText:
		return domain.Part{Kind: domain.PartText, Text: dto.Text}
	case domain.PartThinking:
		if dto.Thinking == nil {
			return domain.Part{Kind: domain.PartThinking, Thinking: &domain.ThinkingPart{}}
		}
		return domain.Part{
			Kind: domain.PartThinking,
			Thinking: &domain.ThinkingPart{
				Text:  dto.Thinking.Text,
				State: domain.CloneRawMessage(dto.Thinking.State),
			},
		}
	case domain.PartToolCall:
		if dto.ToolCall == nil {
			return domain.Part{Kind: domain.PartToolCall}
		}
		return domain.Part{
			Kind: domain.PartToolCall,
			ToolCall: &domain.ToolCall{
				ID:     dto.ToolCall.ID,
				ItemID: dto.ToolCall.ItemID,
				Type:   "function",
				Function: domain.ToolCallFunction{
					Name:      dto.ToolCall.Name,
					Arguments: dto.ToolCall.Arguments,
				},
			},
		}
	case domain.PartToolResult:
		if dto.ToolResult == nil {
			return domain.Part{Kind: domain.PartToolResult}
		}
		return domain.Part{
			Kind: domain.PartToolResult,
			ToolResult: &domain.ToolResultPart{
				ToolCallID: dto.ToolResult.ToolCallID,
				Name:       dto.ToolResult.Name,
				Content:    dto.ToolResult.Content,
				IsError:    dto.ToolResult.IsError,
			},
		}
	default:
		return domain.Part{Kind: domain.PartKind(dto.Kind), Text: dto.Text}
	}
}

// FromDomainTurns converts normalized turns back into the stable transport shape.
func FromDomainTurns(turns []domain.Turn) []TurnDTO {
	dtos := make([]TurnDTO, 0, len(turns))
	for _, turn := range turns {
		dto := TurnDTO{Role: string(turn.Role)}
		for _, part := range turn.Parts {
			dto.Parts = append(dto.Parts, fromDomainPart(part))
		}
		dtos = append(dtos, dto)
	}
	return dtos
}

func fromDomainPart(part domain.Part) PartDTO {
	switch part.Kind {
	case domain.PartText:
		return PartDTO{Kind: "text", Text: part.Text}
	case domain.PartThinking:
		dto := PartDTO{Kind: "thinking"}
		if part.Thinking != nil {
			dto.Thinking = &ThinkingPartDTO{
				Text:  part.Thinking.Text,
				State: domain.CloneRawMessage(part.Thinking.State),
			}
		}
		return dto
	case domain.PartToolCall:
		dto := PartDTO{Kind: "tool_call"}
		if part.ToolCall != nil {
			dto.ToolCall = &ToolCallDTO{
				ID:        part.ToolCall.ID,
				ItemID:    part.ToolCall.ItemID,
				Name:      part.ToolCall.Function.Name,
				Arguments: part.ToolCall.Function.Arguments,
			}
		}
		return dto
	case domain.PartToolResult:
		dto := PartDTO{Kind: "tool_result"}
		if part.ToolResult != nil {
			dto.ToolResult = &ToolResultDTO{
				ToolCallID: part.ToolResult.ToolCallID,
				Name:       part.ToolResult.Name,
				Content:    part.ToolResult.Content,
				IsError:    part.ToolResult.IsError,
			}
		}
		return dto
	default:
		return PartDTO{Kind: string(part.Kind)}
	}
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
		dto.ToolCalls = make([]StreamToolCallDTO, 0, len(event.ToolCalls))
		for _, tc := range event.ToolCalls {
			dto.ToolCalls = append(dto.ToolCalls, StreamToolCallDTO{
				ID:        tc.ID,
				ItemID:    tc.ItemID,
				Type:      tc.Type,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}
	if event.ToolResult != nil {
		dto.ToolResult = &StreamToolResultDTO{
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
		dto.Plan = make([]PlanStepDTO, 0, len(event.Plan))
		for _, step := range event.Plan {
			dto.Plan = append(dto.Plan, PlanStepDTO{Title: step.Title, Status: step.Status})
		}
	}

	return dto
}
