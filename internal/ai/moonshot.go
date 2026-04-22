package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// moonshotProvider implements Provider for the Kimi API.
type moonshotProvider struct {
	client  *http.Client
	baseURL string
}

type moonshotChatRequest struct {
	Model    string            `json:"model"`
	Messages []moonshotMessage `json:"messages"`
	Stream   bool              `json:"stream"`
	Tools    []ToolDefinition  `json:"tools,omitempty"`
	Thinking *moonshotThinking `json:"thinking,omitempty"`
}

type moonshotMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
}

type moonshotThinking struct {
	Type string `json:"type"`
	Keep string `json:"keep,omitempty"`
}

// NewMoonshotProvider creates a new Kimi provider backed by Moonshot AI.
func NewMoonshotProvider() Provider {
	return &moonshotProvider{
		client:  &http.Client{Timeout: 10 * time.Minute},
		baseURL: "https://api.moonshot.ai/v1",
	}
}

func (p *moonshotProvider) ID() string { return "moonshot" }

func (p *moonshotProvider) Models() []Model {
	return []Model{
		{
			ID:         "kimi-k2.6",
			Name:       "Kimi K2.6",
			ProviderID: "moonshot",
			Thinking: ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       true,
			},
		},
	}
}

func buildMoonshotChatRequest(req ChatRequest, model Model) moonshotChatRequest {
	thinkingEnabled := thinkingEnabledForRequest(model, req)
	messages := make([]moonshotMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		moonshotMsg := moonshotMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCalls:  msg.ToolCalls,
			ToolCallID: msg.ToolCallID,
		}

		if thinkingEnabled {
			switch {
			case msg.ReasoningContent != "":
				reasoningContent := msg.ReasoningContent
				moonshotMsg.ReasoningContent = &reasoningContent
			case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
				// Compatibility fallback: preserve the field slot even if an older
				// client did not retain the previous turn's reasoning content.
				reasoningContent := ""
				moonshotMsg.ReasoningContent = &reasoningContent
			}
		}

		messages = append(messages, moonshotMsg)
	}

	body := moonshotChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   true,
		Tools:    req.Tools,
	}
	if model.Thinking.Supported {
		thinking := &moonshotThinking{}
		if thinkingEnabled {
			thinking.Type = "enabled"
			thinking.Keep = "all"
		} else if model.Thinking.CanDisable {
			thinking.Type = "disabled"
		}
		if thinking.Type != "" {
			body.Thinking = thinking
		}
	}

	return body
}

func (p *moonshotProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
	model, _ := modelByID(p.Models(), req.Model)
	payload, err := json.Marshal(buildMoonshotChatRequest(req, model))
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("moonshot API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readSSEStream(resp.Body, ch)
	return ch, nil
}
