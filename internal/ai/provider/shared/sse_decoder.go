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

// chatUsage is a chat completion's usage. prompt_tokens includes the cached
// tokens and, where the provider reports them, the tokens written to its
// cache. Moonshot also reports cached_tokens at the top level.
type chatUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

// ReadSSEStream reads an OpenAI-compatible SSE stream and emits normalized events.
func ReadSSEStream(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var toolCalls ToolCallAccumulator
	var usage *chatUsage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			emitUsage(ch, usage)
			toolCalls.Emit(ch)
			ch <- domain.ProviderEvent{Done: true}
			return
		}

		var chunk struct {
			Usage   *chatUsage `json:"usage"`
			Choices []struct {
				Delta struct {
					ReasoningContent string          `json:"reasoning_content"`
					Content          json.RawMessage `json:"content"`
					ToolCalls        []ToolCallDelta `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		reasoningDelta := choice.Delta.ReasoningContent
		textDelta := textDelta(choice.Delta.Content)
		contentReasoning, contentText := contentDeltas(choice.Delta.Content)
		if contentReasoning != "" {
			reasoningDelta += contentReasoning
		}
		if contentText != "" {
			textDelta += contentText
		}

		if reasoningDelta != "" {
			ch <- domain.ProviderEvent{ReasoningDelta: reasoningDelta}
		}
		if textDelta != "" {
			ch <- domain.ProviderEvent{TextDelta: textDelta}
		}

		for _, delta := range choice.Delta.ToolCalls {
			toolCalls.Add(delta)
		}
	}

	emitUsage(ch, usage)
	if err := scanner.Err(); err != nil {
		log.Printf("error reading SSE stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
		return
	}

	toolCalls.Emit(ch)
	ch <- domain.ProviderEvent{Done: true}
}

// emitUsage sends what the request was billed for, before any error or the
// end of the stream.
func emitUsage(ch chan<- domain.ProviderEvent, usage *chatUsage) {
	if usage == nil {
		return
	}
	cached := usage.PromptTokensDetails.CachedTokens
	if cached == 0 {
		cached = usage.CachedTokens
	}
	ch <- domain.ProviderEvent{Usage: ReportedUsage{
		PromptTokens:     usage.PromptTokens,
		CachedTokens:     cached,
		CacheWriteTokens: usage.PromptTokensDetails.CacheWriteTokens,
		OutputTokens:     usage.CompletionTokens,
	}.Billed()}
}

type contentChunk struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking json.RawMessage `json:"thinking"`
}

func textDelta(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

func contentDeltas(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || textDelta(raw) != "" {
		return "", ""
	}

	var chunks []contentChunk
	if err := json.Unmarshal(raw, &chunks); err == nil {
		return deltasFromContentChunks(chunks)
	}

	var chunk contentChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return "", ""
	}
	return deltasFromContentChunks([]contentChunk{chunk})
}

func deltasFromContentChunks(chunks []contentChunk) (string, string) {
	var reasoning strings.Builder
	var text strings.Builder
	for _, chunk := range chunks {
		switch chunk.Type {
		case "thinking":
			reasoning.WriteString(thinkingText(chunk))
		case "text", "output_text", "":
			text.WriteString(chunk.Text)
		default:
			if chunk.Text != "" {
				text.WriteString(chunk.Text)
			}
		}
	}
	return reasoning.String(), text.String()
}

func thinkingText(chunk contentChunk) string {
	if chunk.Text != "" {
		return chunk.Text
	}
	if len(chunk.Thinking) == 0 {
		return ""
	}

	if text := textDelta(chunk.Thinking); text != "" {
		return text
	}

	var parts []contentChunk
	if err := json.Unmarshal(chunk.Thinking, &parts); err == nil {
		_, text := deltasFromContentChunks(parts)
		return text
	}

	var part contentChunk
	if err := json.Unmarshal(chunk.Thinking, &part); err == nil {
		_, text := deltasFromContentChunks([]contentChunk{part})
		return text
	}
	return ""
}
