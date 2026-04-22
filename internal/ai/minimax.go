package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// minimaxProvider implements Provider for the MiniMax AI API.
type minimaxProvider struct {
	client  *http.Client
	baseURL string
}

type minimaxChatRequest struct {
	Model          string           `json:"model"`
	Messages       []minimaxMessage `json:"messages"`
	Stream         bool             `json:"stream"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	ReasoningSplit bool             `json:"reasoning_split,omitempty"`
}

type minimaxMessage struct {
	Role             string                   `json:"role"`
	Content          string                   `json:"content"`
	ToolCalls        []ToolCall               `json:"tool_calls,omitempty"`
	ToolCallID       string                   `json:"tool_call_id,omitempty"`
	ReasoningDetails []minimaxReasoningDetail `json:"reasoning_details,omitempty"`
}

type minimaxReasoningDetail struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Format string `json:"format,omitempty"`
	Index  int    `json:"index"`
	Text   string `json:"text"`
}

// NewMiniMaxProvider creates a new MiniMax AI provider.
func NewMiniMaxProvider() Provider {
	return &minimaxProvider{
		client:  &http.Client{Timeout: 10 * time.Minute},
		baseURL: "https://api.minimax.io/v1",
	}
}

func (p *minimaxProvider) ID() string { return "minimax" }

func (p *minimaxProvider) Models() []Model {
	return []Model{
		{
			ID:         "MiniMax-M2.7",
			Name:       "MiniMax M2.7",
			ProviderID: "minimax",
			Thinking: ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
			},
		},
	}
}

func buildMiniMaxChatRequest(req ChatRequest, model Model) minimaxChatRequest {
	thinkingEnabled := thinkingEnabledForRequest(model, req)
	messages := make([]minimaxMessage, 0, len(req.Messages))

	for _, msg := range req.Messages {
		minimaxMsg := minimaxMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCalls:  msg.ToolCalls,
			ToolCallID: msg.ToolCallID,
		}

		if thinkingEnabled && msg.Role == "assistant" {
			minimaxMsg.ReasoningDetails = minimaxReasoningDetailsFromMessage(msg)
		}

		messages = append(messages, minimaxMsg)
	}

	body := minimaxChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   true,
		Tools:    req.Tools,
	}
	if thinkingEnabled {
		body.ReasoningSplit = true
	}

	return body
}

func (p *minimaxProvider) ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error) {
	model, _ := modelByID(p.Models(), req.Model)
	payload, err := json.Marshal(buildMiniMaxChatRequest(req, model))
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
		return nil, fmt.Errorf("minimax API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamEvent, 64)
	go readMiniMaxSSEStream(resp.Body, ch)
	return ch, nil
}

func readMiniMaxSSEStream(body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var toolCalls []ToolCall
	toolCallArgs := make(map[int]*strings.Builder)
	var reasoningBuffer string
	var contentBuffer string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			if len(toolCalls) > 0 {
				for i, tc := range toolCalls {
					if b, ok := toolCallArgs[i]; ok {
						tc.Function.Arguments = b.String()
						toolCalls[i] = tc
					}
				}
				ch <- StreamEvent{ToolCalls: toolCalls}
			}
			ch <- StreamEvent{Done: true}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string                   `json:"content"`
					ReasoningDetails []minimaxReasoningDetail `json:"reasoning_details"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		reasoningText := minimaxReasoningText(choice.Delta.ReasoningDetails)
		if reasoningText != "" {
			newReasoning := minimaxStreamDelta(reasoningBuffer, reasoningText)
			reasoningBuffer = reasoningText
			if newReasoning != "" {
				ch <- StreamEvent{ReasoningContent: newReasoning, ThinkingState: minimaxThinkingState(choice.Delta.ReasoningDetails)}
			}
		}

		if choice.Delta.Content != "" {
			newContent := minimaxStreamDelta(contentBuffer, choice.Delta.Content)
			contentBuffer = choice.Delta.Content
			if newContent != "" {
				ch <- StreamEvent{Content: newContent}
			}
		}

		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if tc.ID != "" {
				for len(toolCalls) <= idx {
					toolCalls = append(toolCalls, ToolCall{})
				}
				tcType := tc.Type
				if tcType == "" {
					tcType = "function"
				}
				toolCalls[idx] = ToolCall{
					ID:   tc.ID,
					Type: tcType,
					Function: ToolCallFunction{
						Name: tc.Function.Name,
					},
				}
				toolCallArgs[idx] = &strings.Builder{}
			}
			if tc.Function.Arguments != "" {
				if _, ok := toolCallArgs[idx]; !ok {
					toolCallArgs[idx] = &strings.Builder{}
				}
				toolCallArgs[idx].WriteString(tc.Function.Arguments)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error reading MiniMax SSE stream: %v", err)
		ch <- StreamEvent{Error: "error reading response stream"}
	}

	if len(toolCalls) > 0 {
		for i, tc := range toolCalls {
			if b, ok := toolCallArgs[i]; ok {
				tc.Function.Arguments = b.String()
				toolCalls[i] = tc
			}
		}
		ch <- StreamEvent{ToolCalls: toolCalls}
	}
	ch <- StreamEvent{Done: true}
}

func minimaxReasoningDetailsFromMessage(msg Message) []minimaxReasoningDetail {
	if len(msg.ThinkingState) > 0 {
		var details []minimaxReasoningDetail
		if err := json.Unmarshal(msg.ThinkingState, &details); err == nil && len(details) > 0 {
			return details
		}
	}
	if msg.ReasoningContent == "" {
		return nil
	}

	return []minimaxReasoningDetail{{
		Type:   "reasoning.text",
		ID:     "reasoning-text-1",
		Format: "MiniMax-response-v1",
		Index:  0,
		Text:   msg.ReasoningContent,
	}}
}

func minimaxThinkingState(details []minimaxReasoningDetail) json.RawMessage {
	if len(details) == 0 {
		return nil
	}

	raw, err := json.Marshal(details)
	if err != nil {
		return nil
	}

	return raw
}

func minimaxReasoningText(details []minimaxReasoningDetail) string {
	if len(details) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, detail := range details {
		builder.WriteString(detail.Text)
	}

	return builder.String()
}

func minimaxStreamDelta(previous, current string) string {
	if previous == "" {
		return current
	}
	if strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}

	return current
}
