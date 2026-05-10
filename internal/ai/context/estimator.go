package context

import (
	"strings"
	"unicode/utf8"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const fallbackCharsPerToken = 3

type profile struct {
	charsPerToken          int
	requestOverhead        int
	turnOverhead           int
	partOverhead           int
	toolDefinitionOverhead int
	toolCallOverhead       int
	toolResultOverhead     int
	thinkingOverhead       int
}

// Request is the provider-facing prompt shape to account for before streaming.
type Request struct {
	ProviderID string
	ModelID    string
	Turns      []domain.Turn
	Tools      []domain.ToolDefinition
	Thinking   *domain.ThinkingConfig
}

// Estimate is a deterministic prompt-size estimate, split by the expensive
// transcript sections that later budgeting and compaction need to reason about.
type Estimate struct {
	ProviderID             string
	ModelID                string
	TotalTokens            int
	SystemTokens           int
	ConversationTokens     int
	ToolSchemaTokens       int
	ThinkingTokens         int
	ToolCallArgumentTokens int
	ToolResultTokens       int
	OverheadTokens         int
	Turns                  []TurnEstimate
	Tools                  []ToolEstimate
}

// TurnEstimate is the token breakdown for one normalized turn.
type TurnEstimate struct {
	Role                   domain.Role
	TotalTokens            int
	SystemTokens           int
	ConversationTokens     int
	ThinkingTokens         int
	ToolCallArgumentTokens int
	ToolResultTokens       int
	OverheadTokens         int
	Parts                  []PartEstimate
}

// PartEstimate is the estimated provider-facing size of one turn part.
type PartEstimate struct {
	Kind   domain.PartKind
	Name   string
	Tokens int
}

// ToolEstimate is the estimated provider-facing size of one tool schema.
type ToolEstimate struct {
	Name   string
	Tokens int
}

// Estimator provides deterministic approximate prompt-size accounting.
type Estimator struct{}

// NewEstimator creates a prompt-size estimator.
func NewEstimator() Estimator {
	return Estimator{}
}

// EstimateRequest estimates the provider-facing input size for a chat request.
func EstimateRequest(req Request) Estimate {
	return NewEstimator().Estimate(req)
}

// EstimateChatRequest estimates an existing domain chat request using the
// resolved model's provider metadata.
func EstimateChatRequest(model domain.Model, req domain.ChatRequest) Estimate {
	modelID := req.Model
	if modelID == "" {
		modelID = model.ID
	}

	return EstimateRequest(Request{
		ProviderID: model.ProviderID,
		ModelID:    modelID,
		Turns:      req.Turns,
		Tools:      req.Tools,
		Thinking:   req.Thinking,
	})
}

// EstimateText estimates tokens for free-form text using the model profile, or
// a conservative ceil(chars / 3) fallback for unknown providers and models.
func EstimateText(providerID, modelID, text string) int {
	return tokenCount(profileFor(providerID, modelID), text)
}

// Estimate returns a component breakdown for the request.
func (Estimator) Estimate(req Request) Estimate {
	p := profileFor(req.ProviderID, req.ModelID)
	estimate := Estimate{
		ProviderID:     req.ProviderID,
		ModelID:        req.ModelID,
		OverheadTokens: p.requestOverhead + tokenCount(p, req.ModelID),
		Turns:          make([]TurnEstimate, 0, len(req.Turns)),
		Tools:          make([]ToolEstimate, 0, len(req.Tools)),
	}

	if req.Thinking != nil {
		estimate.OverheadTokens += p.partOverhead + tokenCount(p, req.Thinking.Effort)
		if req.Thinking.Enabled != nil {
			estimate.OverheadTokens++
		}
	}

	for _, tool := range req.Tools {
		toolEstimate := estimateTool(p, tool)
		estimate.Tools = append(estimate.Tools, toolEstimate)
		estimate.ToolSchemaTokens += toolEstimate.Tokens
	}

	for _, turn := range req.Turns {
		turnEstimate := estimateTurn(p, turn)
		estimate.Turns = append(estimate.Turns, turnEstimate)
		estimate.SystemTokens += turnEstimate.SystemTokens
		estimate.ConversationTokens += turnEstimate.ConversationTokens
		estimate.ThinkingTokens += turnEstimate.ThinkingTokens
		estimate.ToolCallArgumentTokens += turnEstimate.ToolCallArgumentTokens
		estimate.ToolResultTokens += turnEstimate.ToolResultTokens
		estimate.OverheadTokens += turnEstimate.OverheadTokens
	}

	estimate.TotalTokens = estimate.SystemTokens +
		estimate.ConversationTokens +
		estimate.ToolSchemaTokens +
		estimate.ThinkingTokens +
		estimate.ToolCallArgumentTokens +
		estimate.ToolResultTokens +
		estimate.OverheadTokens

	return estimate
}

func estimateTool(p profile, tool domain.ToolDefinition) ToolEstimate {
	tokens := p.toolDefinitionOverhead +
		tokenCount(p, tool.Type) +
		tokenCount(p, tool.Function.Name) +
		tokenCount(p, tool.Function.Description) +
		tokenCount(p, string(tool.Function.Parameters))

	return ToolEstimate{
		Name:   tool.Function.Name,
		Tokens: tokens,
	}
}

func estimateTurn(p profile, turn domain.Turn) TurnEstimate {
	estimate := TurnEstimate{
		Role:           turn.Role,
		OverheadTokens: p.turnOverhead + tokenCount(p, string(turn.Role)),
		Parts:          make([]PartEstimate, 0, len(turn.Parts)),
	}

	for _, part := range turn.Parts {
		partEstimate := estimatePart(p, turn.Role, part, &estimate)
		if partEstimate.Tokens > 0 {
			estimate.Parts = append(estimate.Parts, partEstimate)
		}
	}

	estimate.TotalTokens = estimate.SystemTokens +
		estimate.ConversationTokens +
		estimate.ThinkingTokens +
		estimate.ToolCallArgumentTokens +
		estimate.ToolResultTokens +
		estimate.OverheadTokens

	return estimate
}

func estimatePart(p profile, role domain.Role, part domain.Part, turn *TurnEstimate) PartEstimate {
	switch part.Kind {
	case domain.PartText:
		tokens := tokenCount(p, part.Text)
		if tokens == 0 {
			return PartEstimate{}
		}
		turn.OverheadTokens += p.partOverhead
		if role == domain.RoleSystem {
			turn.SystemTokens += tokens
		} else {
			turn.ConversationTokens += tokens
		}
		return PartEstimate{Kind: part.Kind, Tokens: tokens + p.partOverhead}

	case domain.PartThinking:
		if part.Thinking == nil {
			return PartEstimate{}
		}
		tokens := tokenCount(p, part.Thinking.Text) + tokenCount(p, string(part.Thinking.State))
		if tokens == 0 {
			return PartEstimate{}
		}
		overhead := p.partOverhead + p.thinkingOverhead
		turn.ThinkingTokens += tokens
		turn.OverheadTokens += overhead
		return PartEstimate{Kind: part.Kind, Tokens: tokens + overhead}

	case domain.PartToolCall:
		if part.ToolCall == nil {
			return PartEstimate{}
		}
		metadataTokens := tokenCount(p, part.ToolCall.ID) +
			tokenCount(p, part.ToolCall.ItemID) +
			tokenCount(p, part.ToolCall.Type) +
			tokenCount(p, part.ToolCall.Function.Name)
		argTokens := tokenCount(p, part.ToolCall.Function.Arguments)
		overhead := p.partOverhead + p.toolCallOverhead + metadataTokens
		turn.ToolCallArgumentTokens += argTokens
		turn.OverheadTokens += overhead
		return PartEstimate{Kind: part.Kind, Name: part.ToolCall.Function.Name, Tokens: argTokens + overhead}

	case domain.PartToolResult:
		if part.ToolResult == nil {
			return PartEstimate{}
		}
		metadataTokens := tokenCount(p, part.ToolResult.ToolCallID) +
			tokenCount(p, part.ToolResult.Name)
		if part.ToolResult.IsError {
			metadataTokens++
		}
		contentTokens := tokenCount(p, part.ToolResult.Content)
		overhead := p.partOverhead + p.toolResultOverhead + metadataTokens
		turn.ToolResultTokens += contentTokens
		turn.OverheadTokens += overhead
		return PartEstimate{Kind: part.Kind, Name: part.ToolResult.Name, Tokens: contentTokens + overhead}
	default:
		return PartEstimate{}
	}
}

func profileFor(providerID, modelID string) profile {
	p := profile{
		charsPerToken:          fallbackCharsPerToken,
		requestOverhead:        12,
		turnOverhead:           4,
		partOverhead:           2,
		toolDefinitionOverhead: 12,
		toolCallOverhead:       10,
		toolResultOverhead:     10,
		thinkingOverhead:       8,
	}

	provider := strings.ToLower(strings.TrimSpace(providerID))
	model := strings.ToLower(strings.TrimSpace(modelID))

	switch {
	case provider == "anthropic" || strings.Contains(model, "claude"):
		p.requestOverhead = 14
		p.toolDefinitionOverhead = 14
		p.toolCallOverhead = 12
		p.toolResultOverhead = 12
		p.thinkingOverhead = 10
	case provider == "openai" || strings.HasPrefix(model, "gpt-"):
		p.requestOverhead = 12
		p.toolDefinitionOverhead = 12
		p.toolCallOverhead = 10
		p.toolResultOverhead = 10
		p.thinkingOverhead = 8
	case provider == "mistral" || strings.Contains(model, "mistral") || strings.Contains(model, "devstral"):
		p.requestOverhead = 12
	case provider == "moonshot" || strings.Contains(model, "kimi"):
		p.requestOverhead = 12
	case provider == "minimax" || strings.Contains(model, "minimax"):
		p.requestOverhead = 12
	}

	return p
}

func tokenCount(p profile, text string) int {
	if text == "" {
		return 0
	}

	charsPerToken := p.charsPerToken
	if charsPerToken <= 0 {
		charsPerToken = fallbackCharsPerToken
	}

	chars := utf8.RuneCountInString(text)
	return (chars + charsPerToken - 1) / charsPerToken
}
