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
	ToolCall   *ToolCallDTO     `json:"toolCall,omitempty"`
	ToolResult *ToolResultDTO   `json:"toolResult,omitempty"`
	Image      *ImageRefDTO     `json:"image,omitempty"`
}

// ImageRefDTO refers to a stored image attachment.
type ImageRefDTO struct {
	AttachmentID int64  `json:"attachmentId"`
	MediaType    string `json:"mediaType"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
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
	ToolCallID string        `json:"toolCallId"`
	Name       string        `json:"name"`
	Content    string        `json:"content"`
	IsError    bool          `json:"isError,omitempty"`
	Images     []ImageRefDTO `json:"images,omitempty"`
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
	QuestionRequired *QuestionRequestDTO  `json:"questionRequired,omitempty"`
	QuestionResolved *QuestionResultDTO   `json:"questionResolved,omitempty"`
	Plan             []PlanStepDTO        `json:"plan,omitempty"`
	ContextSize      *ContextSizeDTO      `json:"contextSize,omitempty"`
	Usage            *UsageDTO            `json:"usage,omitempty"`
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
	Usage          *UsageDTO  `json:"usage,omitempty"`
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
	ToolCallID string        `json:"toolCallId"`
	Name       string        `json:"name"`
	Content    string        `json:"content"`
	IsError    bool          `json:"isError,omitempty"`
	Images     []ImageRefDTO `json:"images,omitempty"`
}

// ApprovalRequestDTO asks the client to approve a command.
type ApprovalRequestDTO struct {
	ID      string              `json:"id"`
	Title   string              `json:"title,omitempty"`
	Command string              `json:"command"`
	Details []ApprovalDetailDTO `json:"details,omitempty"`
}

// ApprovalResultDTO reports the outcome of a previous approval request.
type ApprovalResultDTO struct {
	ID      string              `json:"id"`
	Title   string              `json:"title,omitempty"`
	Command string              `json:"command"`
	Details []ApprovalDetailDTO `json:"details,omitempty"`
	Status  string              `json:"status"`
}

// ApprovalDetailDTO is one fact about an action awaiting approval. Text
// marks a value the model wrote, to be shown verbatim and set apart.
type ApprovalDetailDTO struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Text  bool   `json:"text,omitempty"`
}

// FromApprovalDetails converts approval details for the client.
func FromApprovalDetails(details []domain.ApprovalDetail) []ApprovalDetailDTO {
	if len(details) == 0 {
		return nil
	}
	out := make([]ApprovalDetailDTO, len(details))
	for i, d := range details {
		out[i] = ApprovalDetailDTO{Label: d.Label, Value: d.Value, Text: d.Text}
	}
	return out
}

// QuestionRequestDTO asks the frontend to collect answers from the user.
type QuestionRequestDTO struct {
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	Questions []QuestionItemDTO `json:"questions"`
}

// QuestionItemDTO carries one question for the frontend.
type QuestionItemDTO struct {
	ID          string              `json:"id"`
	Prompt      string              `json:"prompt"`
	Type        string              `json:"type"`
	Options     []QuestionOptionDTO `json:"options,omitempty"`
	AllowCustom bool                `json:"allowCustom"`
	Placeholder string              `json:"placeholder,omitempty"`
}

// QuestionOptionDTO carries one selectable answer.
type QuestionOptionDTO struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// QuestionAnswerDTO carries one user answer.
type QuestionAnswerDTO struct {
	QuestionID string   `json:"questionId"`
	Values     []string `json:"values,omitempty"`
	Custom     string   `json:"custom,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
}

// QuestionResultDTO reports that a pending user question was resolved.
type QuestionResultDTO struct {
	ID      string              `json:"id"`
	Status  string              `json:"status"`
	Answers []QuestionAnswerDTO `json:"answers,omitempty"`
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

// UsageDTO is the tokens provider requests were billed for and, when the
// model's prices are known, their cost in USD.
type UsageDTO struct {
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64    `json:"cacheWriteTokens,omitempty"`
	CostUSD          *float64 `json:"costUsd,omitempty"`
}

// FromUsage converts usage into its transport shape, or nil when there is
// none to report.
func FromUsage(usage domain.Usage) *UsageDTO {
	if usage == (domain.Usage{}) {
		return nil
	}
	return &UsageDTO{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		CostUSD:          usage.CostUSD,
	}
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
				Images:     toDomainImages(dto.ToolResult.Images),
			},
		}
	case domain.PartImage:
		if dto.Image == nil {
			return domain.Part{Kind: domain.PartImage}
		}
		return domain.NewImagePart(toDomainImage(*dto.Image))
	default:
		return domain.Part{Kind: domain.PartKind(dto.Kind), Text: dto.Text}
	}
}

// toDomainImage takes only the reference: the image's type and size come
// from the stored attachment once a run loads it.
func toDomainImage(dto ImageRefDTO) domain.ImagePart {
	return domain.ImagePart{
		AttachmentID: dto.AttachmentID,
		MediaType:    dto.MediaType,
		Width:        dto.Width,
		Height:       dto.Height,
	}
}

func toDomainImages(dtos []ImageRefDTO) []domain.ImagePart {
	if len(dtos) == 0 {
		return nil
	}
	images := make([]domain.ImagePart, 0, len(dtos))
	for _, dto := range dtos {
		images = append(images, toDomainImage(dto))
	}
	return images
}

func fromDomainImage(image domain.ImagePart) ImageRefDTO {
	return ImageRefDTO{
		AttachmentID: image.AttachmentID,
		MediaType:    image.MediaType,
		Width:        image.Width,
		Height:       image.Height,
	}
}

func fromDomainImages(images []domain.ImagePart) []ImageRefDTO {
	if len(images) == 0 {
		return nil
	}
	dtos := make([]ImageRefDTO, 0, len(images))
	for _, image := range images {
		dtos = append(dtos, fromDomainImage(image))
	}
	return dtos
}

// FromDomainTurns converts normalized turns back into the stable transport shape.
func FromDomainTurns(turns []domain.Turn) []TurnDTO {
	dtos := make([]TurnDTO, 0, len(turns))
	for _, turn := range turns {
		// A turn with no parts must still encode as an empty array, not null:
		// the frontend iterates parts directly.
		dto := TurnDTO{Role: string(turn.Role), Parts: make([]PartDTO, 0, len(turn.Parts))}
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
				Images:     fromDomainImages(part.ToolResult.Images),
			}
		}
		return dto
	case domain.PartImage:
		dto := PartDTO{Kind: "image"}
		if part.Image != nil {
			image := fromDomainImage(*part.Image)
			dto.Image = &image
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
			IsError:    event.ToolResult.IsError,
			Images:     fromDomainImages(event.ToolResult.Images),
		}
	}
	if event.Approval != nil {
		dto.ApprovalRequired = &ApprovalRequestDTO{
			ID:      event.Approval.ID,
			Title:   event.Approval.Title,
			Command: event.Approval.Command,
			Details: FromApprovalDetails(event.Approval.Details),
		}
	}
	if event.ApprovalResult != nil {
		dto.ApprovalResolved = &ApprovalResultDTO{
			ID:      event.ApprovalResult.ID,
			Title:   event.ApprovalResult.Title,
			Command: event.ApprovalResult.Command,
			Details: FromApprovalDetails(event.ApprovalResult.Details),
			Status:  event.ApprovalResult.Status,
		}
	}
	if event.Question != nil {
		dto.QuestionRequired = FromQuestionRequest(event.Question)
	}
	if event.QuestionResult != nil {
		dto.QuestionResolved = FromQuestionResult(event.QuestionResult)
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
	if event.Usage != nil {
		dto.Usage = FromUsage(*event.Usage)
	}

	return dto
}

// FromQuestionRequest converts a domain question request into a transport DTO.
func FromQuestionRequest(request *domain.UserQuestionRequest) *QuestionRequestDTO {
	if request == nil {
		return nil
	}
	dto := &QuestionRequestDTO{
		ID:        request.ID,
		Title:     request.Title,
		Questions: make([]QuestionItemDTO, 0, len(request.Questions)),
	}
	for _, question := range request.Questions {
		item := QuestionItemDTO{
			ID:          question.ID,
			Prompt:      question.Prompt,
			Type:        string(question.Type),
			AllowCustom: question.AllowCustom,
			Placeholder: question.Placeholder,
			Options:     make([]QuestionOptionDTO, 0, len(question.Options)),
		}
		for _, option := range question.Options {
			item.Options = append(item.Options, QuestionOptionDTO{Value: option.Value, Label: option.Label})
		}
		dto.Questions = append(dto.Questions, item)
	}
	return dto
}

// ToDomainQuestionAnswers converts transport answers to domain answers.
func ToDomainQuestionAnswers(dtos []QuestionAnswerDTO) []domain.UserQuestionAnswer {
	answers := make([]domain.UserQuestionAnswer, 0, len(dtos))
	for _, dto := range dtos {
		answers = append(answers, domain.UserQuestionAnswer{
			QuestionID: dto.QuestionID,
			Values:     append([]string(nil), dto.Values...),
			Custom:     dto.Custom,
			Skipped:    dto.Skipped,
		})
	}
	return answers
}

// FromQuestionResult converts a domain question result into a transport DTO.
func FromQuestionResult(result *domain.UserQuestionResult) *QuestionResultDTO {
	if result == nil {
		return nil
	}
	dto := &QuestionResultDTO{
		ID:      result.ID,
		Status:  result.Status,
		Answers: make([]QuestionAnswerDTO, 0, len(result.Answers)),
	}
	for _, answer := range result.Answers {
		dto.Answers = append(dto.Answers, QuestionAnswerDTO{
			QuestionID: answer.QuestionID,
			Values:     append([]string(nil), answer.Values...),
			Custom:     answer.Custom,
			Skipped:    answer.Skipped,
		})
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
		Usage:          FromUsage(run.Usage),
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
		if preview == "" && len(turn.Images()) > 0 {
			preview = "Image"
		}
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
