package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// startAnthropicStream initiates an Anthropic Messages API streaming request.
func startAnthropicStream(ctx context.Context, client *http.Client, url, apiKey string, payload map[string]any) (<-chan domain.ProviderEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s API error (status %d): %s", providerID, resp.StatusCode, string(respBody))
	}

	ch := make(chan domain.ProviderEvent, 64)
	go readAnthropicStream(resp.Body, ch)
	return ch, nil
}

type pendingThinkingBlock struct {
	blockType string // "thinking" or "redacted_thinking"
	thinking  strings.Builder
	signature string
	data      string // for redacted_thinking
}

// readAnthropicStream reads an Anthropic Messages API SSE stream and emits normalized events.
func readAnthropicStream(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var toolCalls []domain.ToolCall
	toolCallArgs := make(map[int]*strings.Builder)
	pendingThinking := make(map[int]*pendingThinkingBlock)
	var completedThinkingBlocks []map[string]any

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") && !strings.HasPrefix(line, "data: ") {
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType := strings.TrimPrefix(line, "event: ")
			if eventType == "message_stop" {
				if len(completedThinkingBlocks) > 0 {
					if raw, err := json.Marshal(completedThinkingBlocks); err == nil {
						ch <- domain.ProviderEvent{ReasoningState: raw}
					}
				}
				emitAnthropicToolCalls(ch, toolCalls, toolCallArgs)
				ch <- domain.ProviderEvent{Done: true}
				return
			}
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var event anthropicEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("error parsing anthropic event: %v", err)
			continue
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock == nil || event.Index == nil {
				continue
			}
			switch event.ContentBlock.Type {
			case "thinking":
				pendingThinking[*event.Index] = &pendingThinkingBlock{blockType: "thinking"}
			case "redacted_thinking":
				pendingThinking[*event.Index] = &pendingThinkingBlock{
					blockType: "redacted_thinking",
					data:      event.ContentBlock.Data,
				}
			case "tool_use":
				index := *event.Index
				for len(toolCalls) <= index {
					toolCalls = append(toolCalls, domain.ToolCall{})
				}
				toolCalls[index] = domain.ToolCall{
					ID:   event.ContentBlock.ID,
					Type: "function",
					Function: domain.ToolCallFunction{
						Name: event.ContentBlock.Name,
					},
				}
				toolCallArgs[index] = &strings.Builder{}
			}

		case "content_block_delta":
			if event.Delta == nil {
				continue
			}
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					ch <- domain.ProviderEvent{TextDelta: event.Delta.Text}
				}
			case "thinking_delta":
				if event.Delta.Thinking != "" {
					ch <- domain.ProviderEvent{ReasoningDelta: event.Delta.Thinking}
					if event.Index != nil {
						if block, ok := pendingThinking[*event.Index]; ok {
							block.thinking.WriteString(event.Delta.Thinking)
						}
					}
				}
			case "signature_delta":
				if event.Delta.Signature != "" && event.Index != nil {
					if block, ok := pendingThinking[*event.Index]; ok {
						block.signature += event.Delta.Signature
					}
				}
			case "input_json_delta":
				if event.Index == nil || event.Delta.PartialJSON == "" {
					continue
				}
				if builder, ok := toolCallArgs[*event.Index]; ok {
					builder.WriteString(event.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			if event.Index == nil {
				continue
			}
			if block, ok := pendingThinking[*event.Index]; ok {
				var thinkingBlock map[string]any
				if block.blockType == "redacted_thinking" {
					thinkingBlock = map[string]any{
						"type": "redacted_thinking",
						"data": block.data,
					}
				} else {
					thinkingBlock = map[string]any{
						"type":      "thinking",
						"thinking":  block.thinking.String(),
						"signature": block.signature,
					}
				}
				completedThinkingBlocks = append(completedThinkingBlocks, thinkingBlock)
				delete(pendingThinking, *event.Index)
			}

		case "error":
			if event.Error != nil {
				ch <- domain.ProviderEvent{Err: errors.New(event.Error.Message)}
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error reading anthropic stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
	}

	if len(completedThinkingBlocks) > 0 {
		if raw, err := json.Marshal(completedThinkingBlocks); err == nil {
			ch <- domain.ProviderEvent{ReasoningState: raw}
		}
	}
	emitAnthropicToolCalls(ch, toolCalls, toolCallArgs)
	ch <- domain.ProviderEvent{Done: true}
}

func emitAnthropicToolCalls(ch chan<- domain.ProviderEvent, toolCalls []domain.ToolCall, toolCallArgs map[int]*strings.Builder) {
	if len(toolCalls) == 0 {
		return
	}

	finalCalls := make([]domain.ToolCall, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		if toolCall.ID == "" && toolCall.Function.Name == "" {
			continue
		}
		if builder, ok := toolCallArgs[index]; ok {
			toolCall.Function.Arguments = builder.String()
		}
		finalCalls = append(finalCalls, toolCall)
	}

	if len(finalCalls) == 0 {
		return
	}

	ch <- domain.ProviderEvent{ToolCalls: finalCalls}
}

// anthropicEvent represents the structure of Anthropic SSE events.
type anthropicEvent struct {
	Type         string            `json:"type"`
	Index        *int              `json:"index,omitempty"`
	ContentBlock *anthropicContent `json:"content_block,omitempty"`
	Delta        *anthropicDelta   `json:"delta,omitempty"`
	Error        *anthropicError   `json:"error,omitempty"`
}

type anthropicContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Data      string `json:"data,omitempty"`      // redacted_thinking
	Signature string `json:"signature,omitempty"` // thinking
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
