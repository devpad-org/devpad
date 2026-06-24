package minimax

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "minimax"
	providerName = "MiniMax"
	baseURL      = "https://api.minimax.io/v1"

	minimaxM3ModelID  = "MiniMax-M3"
	minimaxM27ModelID = "MiniMax-M2.7"
)

// Adapter streams chat completions from MiniMax.
type Adapter struct {
	client  *http.Client
	baseURL string
}

type chatRequest struct {
	Model          string                  `json:"model"`
	Messages       []message               `json:"messages"`
	Stream         bool                    `json:"stream"`
	Tools          []domain.ToolDefinition `json:"tools,omitempty"`
	ReasoningSplit bool                    `json:"reasoning_split,omitempty"`
	Thinking       *thinking               `json:"thinking,omitempty"`
}

type message struct {
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	ToolCalls        []domain.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
}

type reasoningDetail struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Format string `json:"format,omitempty"`
	Index  int    `json:"index"`
	Text   string `json:"text"`
}

type thinking struct {
	Type string `json:"type"`
}

// NewAdapter creates a new MiniMax adapter.
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
			ID:         minimaxM3ModelID,
			Name:       "MiniMax M3",
			ProviderID: providerID,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       true,
			},
		},
		{
			ID:         minimaxM27ModelID,
			Name:       "MiniMax M2.7",
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
	model, _ := domain.ModelByID(a.Models(), req.Model)
	body := buildChatRequest(req, model)
	return shared.StartStreamRequest(ctx, a.client, a.baseURL+"/chat/completions", creds.APIKey, providerID, body, readSSEStream)
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

		minimaxMsg := message{
			Role:      string(turn.Role),
			Content:   turn.Text(),
			ToolCalls: turn.ToolCalls(),
		}

		if thinkingEnabled && turn.Role == domain.RoleAssistant {
			minimaxMsg.ReasoningDetails = reasoningDetailsFromTurn(turn)
		}

		messages = append(messages, minimaxMsg)
	}

	body := chatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   true,
		Tools:    req.Tools,
	}
	if thinkingEnabled {
		body.ReasoningSplit = true
	}
	if thinking := thinkingConfig(model, thinkingEnabled); thinking != nil {
		body.Thinking = thinking
	}

	return body
}

func thinkingConfig(model domain.Model, thinkingEnabled bool) *thinking {
	if !model.Thinking.Supported || !strings.EqualFold(model.ID, minimaxM3ModelID) {
		return nil
	}
	if thinkingEnabled {
		return &thinking{Type: "adaptive"}
	}
	if model.Thinking.CanDisable {
		return &thinking{Type: "disabled"}
	}

	return nil
}

func readSSEStream(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var toolCalls []domain.ToolCall
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
			emitToolCalls(ch, toolCalls, toolCallArgs)
			ch <- domain.ProviderEvent{Done: true}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string            `json:"content"`
					ReasoningDetails []reasoningDetail `json:"reasoning_details"`
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
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		reasoningText := reasoningText(choice.Delta.ReasoningDetails)
		if reasoningText != "" {
			newReasoning := streamDelta(reasoningBuffer, reasoningText)
			reasoningBuffer = reasoningText
			if newReasoning != "" {
				ch <- domain.ProviderEvent{ReasoningDelta: newReasoning, ReasoningState: thinkingState(choice.Delta.ReasoningDetails)}
			}
		}

		if choice.Delta.Content != "" {
			newContent := streamDelta(contentBuffer, choice.Delta.Content)
			contentBuffer = choice.Delta.Content
			if newContent != "" {
				ch <- domain.ProviderEvent{TextDelta: newContent}
			}
		}

		for _, toolCall := range choice.Delta.ToolCalls {
			index := toolCall.Index
			if toolCall.ID != "" {
				for len(toolCalls) <= index {
					toolCalls = append(toolCalls, domain.ToolCall{})
				}

				toolType := toolCall.Type
				if toolType == "" {
					toolType = "function"
				}

				toolCalls[index] = domain.ToolCall{
					ID:   toolCall.ID,
					Type: toolType,
					Function: domain.ToolCallFunction{
						Name: toolCall.Function.Name,
					},
				}
				toolCallArgs[index] = &strings.Builder{}
			}

			if toolCall.Function.Arguments != "" {
				if _, ok := toolCallArgs[index]; !ok {
					toolCallArgs[index] = &strings.Builder{}
				}
				toolCallArgs[index].WriteString(toolCall.Function.Arguments)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error reading MiniMax SSE stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
	}

	emitToolCalls(ch, toolCalls, toolCallArgs)
	ch <- domain.ProviderEvent{Done: true}
}

func reasoningDetailsFromTurn(turn domain.Turn) []reasoningDetail {
	if state := turn.ThinkingState(); len(state) > 0 {
		var details []reasoningDetail
		if err := json.Unmarshal(state, &details); err == nil && len(details) > 0 {
			return details
		}
	}
	if turn.ThinkingText() == "" {
		return nil
	}

	return []reasoningDetail{{
		Type:   "reasoning.text",
		ID:     "reasoning-text-1",
		Format: "MiniMax-response-v1",
		Index:  0,
		Text:   turn.ThinkingText(),
	}}
}

func thinkingState(details []reasoningDetail) json.RawMessage {
	if len(details) == 0 {
		return nil
	}

	raw, err := json.Marshal(details)
	if err != nil {
		return nil
	}

	return raw
}

func reasoningText(details []reasoningDetail) string {
	if len(details) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, detail := range details {
		builder.WriteString(detail.Text)
	}

	return builder.String()
}

func streamDelta(previous, current string) string {
	if previous == "" {
		return current
	}
	if strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}

	return current
}

func emitToolCalls(ch chan<- domain.ProviderEvent, toolCalls []domain.ToolCall, toolCallArgs map[int]*strings.Builder) {
	if len(toolCalls) == 0 {
		return
	}

	finalCalls := make([]domain.ToolCall, len(toolCalls))
	copy(finalCalls, toolCalls)
	for index, toolCall := range finalCalls {
		if builder, ok := toolCallArgs[index]; ok {
			toolCall.Function.Arguments = builder.String()
			finalCalls[index] = toolCall
		}
	}

	ch <- domain.ProviderEvent{ToolCalls: finalCalls}
}
