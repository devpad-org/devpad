package shared

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ReadSSEStream reads an OpenAI-compatible SSE stream and emits normalized events.
func ReadSSEStream(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var toolCalls []domain.ToolCall
	toolCallArgs := make(map[int]*strings.Builder)

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
					ReasoningContent string `json:"reasoning_content"`
					Content          string `json:"content"`
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
		if choice.Delta.ReasoningContent != "" {
			ch <- domain.ProviderEvent{ReasoningDelta: choice.Delta.ReasoningContent}
		}
		if choice.Delta.Content != "" {
			ch <- domain.ProviderEvent{TextDelta: choice.Delta.Content}
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
		log.Printf("error reading SSE stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
	}

	emitToolCalls(ch, toolCalls, toolCallArgs)
	ch <- domain.ProviderEvent{Done: true}
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
