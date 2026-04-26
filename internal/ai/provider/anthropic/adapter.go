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

		var content []map[string]any

		for _, part := range turn.Parts {
			switch part.Kind {
			case domain.PartThinking:
				// Re-inject prior thinking blocks verbatim for multi-turn continuity.
				if part.Thinking != nil && len(part.Thinking.State) > 0 {
					var blocks []map[string]any
					if err := json.Unmarshal(part.Thinking.State, &blocks); err == nil {
						content = append(content, blocks...)
					}
				}
			case domain.PartText:
				if part.Text != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": part.Text,
					})
				}
			case domain.PartToolCall:
				if part.ToolCall == nil {
					continue
				}
				var input any
				if part.ToolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(part.ToolCall.Function.Arguments), &input); err != nil {
						input = map[string]any{}
					}
				} else {
					input = map[string]any{}
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    part.ToolCall.ID,
					"name":  part.ToolCall.Function.Name,
					"input": input,
				})
			case domain.PartToolResult:
				if part.ToolResult == nil || part.ToolResult.ToolCallID == "" {
					continue
				}
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": part.ToolResult.ToolCallID,
					"content":     part.ToolResult.Content,
				})
			}
		}

		if len(content) == 0 {
			continue
		}

		anthropicMsg := map[string]any{"role": string(turn.Role)}
		// Simplify to plain string when there is exactly one text block.
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
