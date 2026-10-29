package moonshot

import (
	"context"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "moonshot"
	providerName = "Moonshot AI"
	baseURL      = "https://api.moonshot.ai/v1"

	// kimi-k3 always reasons, at the effort set by a top-level
	// reasoning_effort field.
	k3ModelID              = "kimi-k3"
	defaultReasoningEffort = "max"
)

// Adapter streams chat completions from Moonshot's Kimi API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

type chatRequest struct {
	Model           string                  `json:"model"`
	Messages        []message               `json:"messages"`
	Stream          bool                    `json:"stream"`
	Tools           []domain.ToolDefinition `json:"tools,omitempty"`
	ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	StreamOptions   *streamOptions          `json:"stream_options,omitempty"`
}

// streamOptions asks for the request's usage in a final chunk, which Moonshot
// leaves out of the stream by default.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type message struct {
	Role             string            `json:"role"`
	Content          any               `json:"content"` // a string, or content parts with images
	ToolCalls        []domain.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningContent *string           `json:"reasoning_content,omitempty"`
}

// NewAdapter creates a new Moonshot adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		client:  shared.NewStreamingClient(),
		baseURL: baseURL,
	}
}

func (a *Adapter) ProviderID() string   { return providerID }
func (a *Adapter) ProviderName() string { return providerName }

func (a *Adapter) Protocol() aiprovider.Protocol {
	return aiprovider.ProtocolOpenAIChat
}

func (a *Adapter) Models() []domain.Model {
	// Prices are USD per million tokens from
	// https://platform.kimi.ai/docs/pricing/chat (October 2026). Caching is
	// automatic with a 5-minute TTL, and K3 bills cache writes at the 5-minute
	// rate.
	return []domain.Model{
		{
			ID:         k3ModelID,
			Name:       "Kimi K3",
			ProviderID: providerID,
			Vision:     true,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
				SupportedEfforts: []string{"low", "high", "max"},
				DefaultEffort:    defaultReasoningEffort,
			},
			Pricing: &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3}},
		},
	}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	model, _ := domain.ModelByID(a.Models(), req.Model)
	body := buildChatRequest(req, model)
	return shared.StartStreamRequest(ctx, a.client, a.baseURL+"/chat/completions", creds.APIKey, providerID, body, shared.ReadSSEStream)
}

func buildChatRequest(req aiprovider.StreamRequest, model domain.Model) chatRequest {
	thinkingEnabled := domain.ThinkingEnabledForRequest(model, domain.ChatRequest{Model: req.Model, Turns: req.Turns, Thinking: req.Thinking})
	// Models that cannot turn thinking off always expect their reasoning
	// history back; K3 in particular degrades badly without it.
	preserveReasoning := model.Thinking.Supported && (thinkingEnabled || !model.Thinking.CanDisable)
	messages := make([]message, 0, len(req.Turns))
	// Images tools returned go in a user message after the run of tool
	// messages, which must directly follow the assistant's tool calls.
	var toolImages []domain.ToolResultPart
	flushToolImages := func() {
		if content, ok := shared.ToolImagesContent(toolImages, shared.ImageURLObject); ok {
			messages = append(messages, message{Role: "user", Content: content})
		}
		toolImages = nil
	}
	for _, turn := range req.Turns {
		if turn.Role == domain.RoleUser {
			toolResults := turn.ToolResults()
			if len(toolResults) > 0 {
				toolImages = append(toolImages, toolResults...)
				for _, result := range toolResults {
					messages = append(messages, message{
						Role:       "tool",
						Content:    result.Content,
						ToolCallID: result.ToolCallID,
					})
				}
				if text := turn.Text(); text != "" {
					messages = append(messages, message{Role: "user", Content: text})
				}
				continue
			}
		}

		flushToolImages()
		moonshotMsg := message{
			Role:      string(turn.Role),
			Content:   shared.ChatContent(turn.Text(), turn.Images(), shared.ImageURLObject),
			ToolCalls: turn.ToolCalls(),
		}

		thinkingText := turn.ThinkingText()
		if preserveReasoning {
			switch {
			case thinkingText != "":
				reasoningContent := thinkingText
				moonshotMsg.ReasoningContent = &reasoningContent
			case turn.Role == domain.RoleAssistant && len(turn.ToolCalls()) > 0:
				reasoningContent := ""
				moonshotMsg.ReasoningContent = &reasoningContent
			}
		}

		messages = append(messages, moonshotMsg)
	}
	flushToolImages()

	body := chatRequest{
		Model:         req.Model,
		Messages:      messages,
		Stream:        true,
		Tools:         req.Tools,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if model.Thinking.Supported {
		body.ReasoningEffort = requestedReasoningEffort(model, req.Thinking)
	}

	return body
}

func requestedReasoningEffort(model domain.Model, thinkingCfg *domain.ThinkingConfig) string {
	if thinkingCfg != nil {
		if effort := strings.TrimSpace(thinkingCfg.Effort); effort != "" {
			return effort
		}
	}
	if model.Thinking.DefaultEffort != "" {
		return model.Thinking.DefaultEffort
	}

	return defaultReasoningEffort
}
