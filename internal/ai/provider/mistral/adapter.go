package mistral

import (
	"context"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "mistral"
	providerName = "Mistral AI"
	baseURL      = "https://api.mistral.ai/v1"

	defaultReasoningEffort  = "high"
	disabledReasoningEffort = "none"
)

// Adapter streams OpenAI-compatible chat completions from Mistral.
type Adapter struct {
	client  *http.Client
	baseURL string
}

// NewAdapter creates a new Mistral adapter.
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
	mistralMediumThinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: false,
		CanDisable:       true,
		SupportedEfforts: []string{defaultReasoningEffort},
		DefaultEffort:    defaultReasoningEffort,
	}

	return []domain.Model{
		{ID: "devstral-medium-latest", Name: "Devstral", ProviderID: providerID},
		{ID: "mistral-medium-3-5", Name: "Mistral Medium 3.5", ProviderID: providerID, Thinking: mistralMediumThinking},
		{ID: "mistral-large-latest", Name: "Mistral Large", ProviderID: providerID},
	}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	model, _ := domain.ModelByID(a.Models(), req.Model)
	body := buildChatRequest(req, model)

	return shared.StartStreamRequest(ctx, a.client, a.baseURL+"/chat/completions", creds.APIKey, providerID, body, shared.ReadSSEStream)
}

func buildChatRequest(req aiprovider.StreamRequest, model domain.Model) map[string]any {
	messages := make([]map[string]any, 0, len(req.Turns))
	for _, turn := range req.Turns {
		if turn.Role == domain.RoleUser {
			toolResults := turn.ToolResults()
			if len(toolResults) > 0 {
				for _, result := range toolResults {
					messages = append(messages, map[string]any{
						"role":         "tool",
						"tool_call_id": result.ToolCallID,
						"content":      result.Content,
					})
				}
				if text := turn.Text(); text != "" {
					messages = append(messages, map[string]any{"role": "user", "content": text})
				}
				continue
			}
		}
		message := map[string]any{
			"role":    string(turn.Role),
			"content": turn.Text(),
		}
		if toolCalls := turn.ToolCalls(); len(toolCalls) > 0 {
			message["tool_calls"] = toolCalls
		}
		messages = append(messages, message)
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if effort := reasoningEffort(req, model); effort != "" {
		body["reasoning_effort"] = effort
	}

	return body
}

func reasoningEffort(req aiprovider.StreamRequest, model domain.Model) string {
	if !model.Thinking.Supported {
		return ""
	}

	enabled := domain.ThinkingEnabledForRequest(model, domain.ChatRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
	})
	if !enabled {
		if req.Thinking != nil && req.Thinking.Enabled != nil && !*req.Thinking.Enabled && model.Thinking.CanDisable {
			return disabledReasoningEffort
		}
		return ""
	}

	if req.Thinking != nil {
		if effort := strings.TrimSpace(req.Thinking.Effort); effort != "" {
			return effort
		}
	}
	if model.Thinking.DefaultEffort != "" {
		return model.Thinking.DefaultEffort
	}

	return defaultReasoningEffort
}
