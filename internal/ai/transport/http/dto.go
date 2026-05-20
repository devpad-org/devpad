package httptransport

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const agentRunPromptPreviewMaxRunes = 120

// ChatRequestDTO is the frontend request contract for chat endpoints.
type ChatRequestDTO struct {
	Model       string       `json:"model"`
	AgentID     string       `json:"agentId,omitempty"`
	Turns       []TurnDTO    `json:"turns"`
	Thinking    *ThinkingDTO `json:"thinking,omitempty"`
	WorkspaceID int64        `json:"workspaceId,omitempty"`
}

// CreateAgentRunRequestDTO is the frontend request contract for background agent runs.
type CreateAgentRunRequestDTO struct {
	Model          string       `json:"model"`
	AgentID        string       `json:"agentId,omitempty"`
	Turns          []TurnDTO    `json:"turns"`
	Thinking       *ThinkingDTO `json:"thinking,omitempty"`
	WorkspaceID    int64        `json:"workspaceId,omitempty"`
	ConversationID int64        `json:"conversationId,omitempty"`
	ParentRunID    int64        `json:"parentRunId,omitempty"`
}

// ThinkingDTO controls thinking mode over the transport boundary.
type ThinkingDTO struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// TurnDTO is the stable frontend transport shape for a conversation turn.
type TurnDTO struct {
	Role  string    `json:"role"`
	Parts []PartDTO `json:"parts"`
}

// PartDTO carries one normalized piece of a turn.
type PartDTO struct {
	Kind       string           `json:"kind"`
	Text       string           `json:"text,omitempty"`
	Thinking   *ThinkingPartDTO `json:"thinking,omitempty"`
	Image      *ImagePartDTO    `json:"image,omitempty"`
	ToolCall   *ToolCallDTO     `json:"toolCall,omitempty"`
	ToolResult *ToolResultDTO   `json:"toolResult,omitempty"`
}

// ThinkingPartDTO carries the reasoning text and opaque provider state.
type ThinkingPartDTO struct {
	Text  string          `json:"text,omitempty"`
	State json.RawMessage `json:"state,omitempty"`
}

// ImagePartDTO carries a base64-encoded pasted image.
type ImagePartDTO struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
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
	RunID            int64                `json:"runId,omitempty"`
	Sequence         int64                `json:"sequence,omitempty"`
	ReasoningContent string               `json:"reasoningContent,omitempty"`
	ThinkingState    json.RawMessage      `json:"thinkingState,omitempty"`
	Content          string               `json:"content,omitempty"`
	ToolCalls        []StreamToolCallDTO  `json:"toolCalls,omitempty"`
	ToolResult       *StreamToolResultDTO `json:"toolResult,omitempty"`
	ApprovalRequired *ApprovalRequestDTO  `json:"approvalRequired,omitempty"`
	ApprovalResolved *ApprovalResultDTO   `json:"approvalResolved,omitempty"`
	Plan             []PlanStepDTO        `json:"plan,omitempty"`
	ContextSize      *ContextSizeDTO      `json:"contextSize,omitempty"`
	Done             bool                 `json:"done,omitempty"`
	Error            string               `json:"error,omitempty"`
}

// AgentRunDTO is the HTTP representation of a background agent run.
type AgentRunDTO struct {
	ID             int64      `json:"id"`
	ParentRunID    int64      `json:"parentRunId,omitempty"`
	UserID         int64      `json:"userId"`
	WorkspaceID    int64      `json:"workspaceId"`
	ConversationID int64      `json:"conversationId,omitempty"`
	PromptPreview  string     `json:"promptPreview,omitempty"`
	AgentID        string     `json:"agentId"`
	Model          string     `json:"model"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	InputTurns     []TurnDTO  `json:"inputTurns,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
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

// ApprovalResultDTO reports the outcome of a previous approval request.
type ApprovalResultDTO struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

// PlanStepDTO is the frontend transport representation of an execution plan step.
type PlanStepDTO struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ContextSizeDTO is numeric-only context pressure telemetry for the UI.
type ContextSizeDTO struct {
	Approximate                bool    `json:"approximate"`
	ProviderID                 string  `json:"providerId,omitempty"`
	Model                      string  `json:"model,omitempty"`
	NextRequestTokens          int     `json:"nextRequestTokens"`
	TotalTranscriptTokens      int     `json:"totalTranscriptTokens"`
	ProviderFacingTokens       int     `json:"providerFacingTokens"`
	InputBudgetTokens          int     `json:"inputBudgetTokens"`
	PercentageUsed             float64 `json:"percentageUsed"`
	WarningThresholdPercentage int     `json:"warningThresholdPercentage"`
	Warning                    bool    `json:"warning"`
}

// ToDomainThinking maps a transport thinking config into the domain shape.
func ToDomainThinking(dto *ThinkingDTO) *domain.ThinkingConfig {
	if dto == nil {
		return nil
	}
	return &domain.ThinkingConfig{Enabled: dto.Enabled, Effort: dto.Effort}
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
	case domain.PartImage:
		if dto.Image == nil {
			return domain.Part{Kind: domain.PartImage}
		}
		return domain.Part{
			Kind: domain.PartImage,
			Image: &domain.ImagePart{
				MIMEType: dto.Image.MIMEType,
				Data:     domain.NormalizeImageData(dto.Image.Data),
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
	case domain.PartImage:
		dto := PartDTO{Kind: "image"}
		if part.Image != nil {
			dto.Image = &ImagePartDTO{MIMEType: part.Image.MIMEType, Data: part.Image.Data}
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
	if event.ApprovalResult != nil {
		dto.ApprovalResolved = &ApprovalResultDTO{
			ID:      event.ApprovalResult.ID,
			Command: event.ApprovalResult.Command,
			Status:  event.ApprovalResult.Status,
		}
	}
	if len(event.Plan) > 0 {
		dto.Plan = make([]PlanStepDTO, 0, len(event.Plan))
		for _, step := range event.Plan {
			dto.Plan = append(dto.Plan, PlanStepDTO{Title: step.Title, Status: step.Status})
		}
	}
	if event.ContextSize != nil {
		dto.ContextSize = &ContextSizeDTO{
			Approximate:                event.ContextSize.Approximate,
			ProviderID:                 event.ContextSize.ProviderID,
			Model:                      event.ContextSize.Model,
			NextRequestTokens:          event.ContextSize.NextRequestTokens,
			TotalTranscriptTokens:      event.ContextSize.TotalTranscriptTokens,
			ProviderFacingTokens:       event.ContextSize.ProviderFacingTokens,
			InputBudgetTokens:          event.ContextSize.InputBudgetTokens,
			PercentageUsed:             event.ContextSize.PercentageUsed,
			WarningThresholdPercentage: event.ContextSize.WarningThresholdPercentage,
			Warning:                    event.ContextSize.Warning,
		}
	}

	return dto
}

// FromAgentRun converts run metadata into the stable transport shape.
func FromAgentRun(run *domain.AgentRun) AgentRunDTO {
	return fromAgentRun(run, true)
}

// FromAgentRunSummary converts run metadata without the full input conversation.
func FromAgentRunSummary(run *domain.AgentRun) AgentRunDTO {
	return fromAgentRun(run, false)
}

func fromAgentRun(run *domain.AgentRun, includeInputTurns bool) AgentRunDTO {
	if run == nil {
		return AgentRunDTO{}
	}
	dto := AgentRunDTO{
		ID:             run.ID,
		ParentRunID:    run.ParentRunID,
		UserID:         run.UserID,
		WorkspaceID:    run.WorkspaceID,
		ConversationID: run.ConversationID,
		PromptPreview:  AgentRunPromptPreview(run.InputTurns),
		AgentID:        run.AgentID,
		Model:          run.Model,
		Status:         string(run.Status),
		Error:          run.Error,
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
	}
	if includeInputTurns && len(run.InputTurns) > 0 {
		dto.InputTurns = FromDomainTurns(run.InputTurns)
	}
	return dto
}

// AgentRunPromptPreview returns a lightweight display title derived from the
// last user-authored text turn in a run. It deliberately avoids exposing the
// full input conversation in list responses.
func AgentRunPromptPreview(turns []domain.Turn) string {
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn.Role != domain.RoleUser {
			continue
		}
		preview := strings.Join(strings.Fields(turn.Text()), " ")
		if preview == "" {
			continue
		}
		return truncateRunes(preview, agentRunPromptPreviewMaxRunes)
	}
	return ""
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// FromAgentRunEvent converts a persisted agent event into the SSE payload shape.
func FromAgentRunEvent(event domain.AgentRunEvent) StreamEventDTO {
	dto := FromClientEvent(event.Event)
	dto.RunID = event.RunID
	dto.Sequence = event.Sequence
	return dto
}
