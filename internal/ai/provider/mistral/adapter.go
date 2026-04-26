package mistral

import (
	"context"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "mistral"
	providerName = "Mistral AI"
	baseURL      = "https://api.mistral.ai/v1"
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
	return []domain.Model{
		{ID: "devstral-medium-latest", Name: "Devstral", ProviderID: providerID},
		{ID: "mistral-large-latest", Name: "Mistral Large", ProviderID: providerID},
	}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	messages := make([]map[string]any, 0, len(req.Turns))
	for _, turn := range req.Turns {
		message := map[string]any{
			"role":    string(turn.Role),
			"content": turn.Text(),
		}
		if toolCalls := turn.ToolCalls(); len(toolCalls) > 0 {
			message["tool_calls"] = toolCalls
		}
		if toolCallID := turn.ToolCallID(); toolCallID != "" {
			message["tool_call_id"] = toolCallID
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

	return shared.StartStreamRequest(ctx, a.client, a.baseURL+"/chat/completions", creds.APIKey, providerID, body, shared.ReadSSEStream)
}
