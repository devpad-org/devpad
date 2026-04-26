package openaichat

import (
	"context"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "openai"
	providerName = "OpenAI"
	baseURL      = "https://api.openai.com/v1"
)

// Adapter streams chat completions from OpenAI's chat-completions API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

// NewAdapter creates a new OpenAI chat-completions adapter.
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
	return []domain.Model{{ID: "gpt-5.4", Name: "GPT-5.4", ProviderID: providerID}}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	messages := buildChatMessages(req.Turns)

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

// buildChatMessages converts domain turns into OpenAI Chat Completions message objects.
// User turns that contain tool results are expanded into individual role=tool messages.
func buildChatMessages(turns []domain.Turn) []map[string]any {
	messages := make([]map[string]any, 0, len(turns))
	for _, turn := range turns {
		switch turn.Role {
		case domain.RoleUser:
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
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": text,
					})
				}
			} else {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": turn.Text(),
				})
			}
		default:
			message := map[string]any{
				"role":    string(turn.Role),
				"content": turn.Text(),
			}
			if toolCalls := turn.ToolCalls(); len(toolCalls) > 0 {
				message["tool_calls"] = toolCalls
			}
			messages = append(messages, message)
		}
	}
	return messages
}
