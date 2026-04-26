package anthropic

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "anthropic"
	providerName = "Anthropic"
	baseURL      = "https://api.anthropic.com/v1"
	apiVersion   = "2023-06-01"
)

// Adapter streams chat completions from Anthropic's Messages API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

// NewAdapter creates a new Anthropic Messages API adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		client:  shared.NewStreamingClient(),
		baseURL: baseURL,
	}
}

func (a *Adapter) ProviderID() string   { return providerID }
func (a *Adapter) ProviderName() string { return providerName }

func (a *Adapter) Protocol() aiprovider.Protocol {
	return aiprovider.ProtocolAnthropic
}

func (a *Adapter) Models() []domain.Model {
	return []domain.Model{
		{
			ID:         "claude-3-5-sonnet-20241022",
			Name:       "Claude 3.5 Sonnet",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: false,
				CanDisable:       true,
			},
		},
		{
			ID:         "claude-3-7-sonnet-20250219",
			Name:       "Claude 3.7 Sonnet",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: false,
				CanDisable:       true,
			},
		},
		{
			ID:         "claude-sonnet-4-20250514",
			Name:       "Claude Sonnet 4",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
			},
		},
	}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	body := buildAnthropicRequest(req)
	return startAnthropicStream(ctx, a.client, a.baseURL+"/messages", creds.APIKey, body)
}

// buildAnthropicRequest converts the provider StreamRequest into an Anthropic-specific payload.
func buildAnthropicRequest(req aiprovider.StreamRequest) map[string]any {
	// Extract system message if present
	var systemPrompt string
	var messages []map[string]any

	for _, turn := range req.Turns {
		if turn.Role == domain.RoleSystem {
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += turn.Text()
			continue
		}

		// Convert to Anthropic message format
		anthropicMsg := map[string]any{
			"role": string(turn.Role),
		}

		// Build content blocks
		var content []map[string]any

		// Handle tool result messages
		if toolResult := turn.ToolResult(); toolResult != nil && toolResult.ToolCallID != "" {
			// Anthropic expects tool results inside a user message, even though the
			// current Devpad runtime stores them as role=tool.
			anthropicMsg["role"] = "user"
			content = append(content, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolResult.ToolCallID,
				"content":     toolResult.Content,
			})
		} else {
			// Add text content if present (not a tool result message)
			if turn.Text() != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": turn.Text(),
				})
			}

			// Add tool use blocks if present
			for _, toolCall := range turn.ToolCalls() {
				// Parse arguments JSON
				var input any
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
						// If parsing fails, use empty object
						input = map[string]any{}
					}
				} else {
					input = map[string]any{}
				}

				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    toolCall.ID,
					"name":  toolCall.Function.Name,
					"input": input,
				})
			}
		}

		// If we only have one text block, simplify to string
		if len(content) == 1 && content[0]["type"] == "text" {
			anthropicMsg["content"] = turn.Text()
		} else {
			anthropicMsg["content"] = content
		}

		messages = append(messages, anthropicMsg)
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": 8192,
		"stream":     true,
	}

	if systemPrompt != "" {
		body["system"] = systemPrompt
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = map[string]any{
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": tool.Function.Parameters,
			}
		}
		body["tools"] = tools
	}

	// Add thinking configuration if present
	if req.Thinking != nil && req.Thinking.Enabled != nil {
		if *req.Thinking.Enabled {
			body["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": 10000,
			}
		} else {
			body["thinking"] = map[string]any{
				"type": "disabled",
			}
		}
	}

	return body
}
