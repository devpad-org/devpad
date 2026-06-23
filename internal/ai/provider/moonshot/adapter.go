package moonshot

import (
	"context"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "moonshot"
	providerName = "Moonshot AI"
	baseURL      = "https://api.moonshot.ai/v1"
)

// Adapter streams chat completions from Moonshot's Kimi API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

type chatRequest struct {
	Model    string                  `json:"model"`
	Messages []message               `json:"messages"`
	Stream   bool                    `json:"stream"`
	Tools    []domain.ToolDefinition `json:"tools,omitempty"`
	Thinking *thinking               `json:"thinking,omitempty"`
}

type message struct {
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	ToolCalls        []domain.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningContent *string           `json:"reasoning_content,omitempty"`
}

type thinking struct {
	Type string `json:"type"`
	Keep string `json:"keep,omitempty"`
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
	return []domain.Model{
		{
			ID:         "kimi-k2.7-code",
			Name:       "Kimi K2.7 Code",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
			},
		},
		{
			ID:         "kimi-k2.6",
			Name:       "Kimi K2.6",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       true,
			},
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
	messages := make([]message, 0, len(req.Turns))
	for _, turn := range req.Turns {
		if turn.Role == domain.RoleUser {
			toolResults := turn.ToolResults()
			if len(toolResults) > 0 {
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

		moonshotMsg := message{
			Role:      string(turn.Role),
			Content:   turn.Text(),
			ToolCalls: turn.ToolCalls(),
		}

		thinkingText := turn.ThinkingText()
		if thinkingEnabled {
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

	body := chatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   true,
		Tools:    req.Tools,
	}
	if model.Thinking.Supported {
		cfg := &thinking{}
		if thinkingEnabled {
			cfg.Type = "enabled"
			cfg.Keep = "all"
		} else if model.Thinking.CanDisable {
			cfg.Type = "disabled"
		}
		if cfg.Type != "" {
			body.Thinking = cfg
		}
	}

	return body
}
