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
func startAnthropicStream(ctx context.Context, client *http.Client, url, apiKey string, payload map[string]any, betas []string) (<-chan domain.ProviderEvent, error) {
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
	if len(betas) > 0 {
		httpReq.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}

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

// The reasoning state saved with an assistant turn is a JSON array of the
// turn's content blocks in the order the model produced them. Thinking blocks
// are stored in full; text and tool_use blocks are stored as references to the
// turn's own text and tool calls, so they aren't duplicated. Replaying the turn
// in its original order keeps the thinking blocks valid on models that bind
// them to the conversation.
const (
	// layoutTextRefType marks a text block holding the next "bytes" bytes of
	// the turn's text.
	layoutTextRefType = "devpad_text_ref"
	// layoutToolUseRefType marks the tool_use block for the tool call "id".
	layoutToolUseRefType = "devpad_tool_use_ref"
)

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
	pendingTextBytes := make(map[int]int)
	var layout []map[string]any
	var usage *domain.Usage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") && !strings.HasPrefix(line, "data: ") {
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType := strings.TrimPrefix(line, "event: ")
			if eventType == "message_stop" {
				emitAnthropicUsage(ch, usage)
				emitAnthropicReasoningState(ch, layout)
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
		case "message_start":
			if event.Message != nil && event.Message.Usage != nil {
				usage = mergeAnthropicUsage(usage, event.Message.Usage)
			}

		case "content_block_start":
			if event.ContentBlock == nil || event.Index == nil {
				continue
			}
			switch event.ContentBlock.Type {
			case "fallback":
				// A safety classifier declined the request part-way and a
				// fallback model continues on the same stream. Thinking and
				// tool_use blocks before the boundary must not be echoed back
				// or executed; streamed text is kept as continuation context.
				toolCalls = nil
				clear(toolCallArgs)
				clear(pendingThinking)
				layout = textRefsOnly(layout)
			case "text":
				pendingTextBytes[*event.Index] = 0
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
					if event.Index != nil {
						if _, ok := pendingTextBytes[*event.Index]; ok {
							pendingTextBytes[*event.Index] += len(event.Delta.Text)
						}
					}
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
			index := *event.Index
			if n, ok := pendingTextBytes[index]; ok {
				if n > 0 {
					layout = append(layout, map[string]any{"type": layoutTextRefType, "bytes": n})
				}
				delete(pendingTextBytes, index)
			}
			if _, ok := toolCallArgs[index]; ok && index < len(toolCalls) && toolCalls[index].ID != "" {
				layout = append(layout, map[string]any{"type": layoutToolUseRefType, "id": toolCalls[index].ID})
			}
			if block, ok := pendingThinking[index]; ok {
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
				layout = append(layout, thinkingBlock)
				delete(pendingThinking, index)
			}

		case "message_delta":
			if event.Usage != nil {
				usage = mergeAnthropicUsage(usage, event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason == "refusal" {
				emitAnthropicUsage(ch, usage)
				ch <- domain.ProviderEvent{Err: refusalError(event.Delta.StopDetails)}
				return
			}

		case "error":
			if event.Error != nil {
				emitAnthropicUsage(ch, usage)
				ch <- domain.ProviderEvent{Err: errors.New(event.Error.Message)}
				return
			}
		}
	}

	emitAnthropicUsage(ch, usage)
	if err := scanner.Err(); err != nil {
		log.Printf("error reading anthropic stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
	}

	emitAnthropicReasoningState(ch, layout)
	emitAnthropicToolCalls(ch, toolCalls, toolCallArgs)
	ch <- domain.ProviderEvent{Done: true}
}

// emitAnthropicReasoningState sends the turn's layout when it holds thinking
// blocks; without them there is nothing to replay.
func emitAnthropicReasoningState(ch chan<- domain.ProviderEvent, layout []map[string]any) {
	hasThinking := false
	for _, block := range layout {
		if block["type"] == "thinking" || block["type"] == "redacted_thinking" {
			hasThinking = true
			break
		}
	}
	if !hasThinking {
		return
	}
	if raw, err := json.Marshal(layout); err == nil {
		ch <- domain.ProviderEvent{ReasoningState: raw}
	}
}

// mergeAnthropicUsage folds a usage report into the request's usage so far.
// message_start reports the prompt and message_delta the running totals, so
// each count the report carries replaces the one before it.
func mergeAnthropicUsage(usage *domain.Usage, report *anthropicUsage) *domain.Usage {
	if usage == nil {
		usage = &domain.Usage{}
	}
	if report.InputTokens > 0 {
		usage.InputTokens = report.InputTokens
	}
	if report.OutputTokens > 0 {
		usage.OutputTokens = report.OutputTokens
	}
	if report.CacheReadInputTokens > 0 {
		usage.CacheReadTokens = report.CacheReadInputTokens
	}
	if report.CacheCreationInputTokens > 0 {
		usage.CacheWriteTokens = report.CacheCreationInputTokens
	}
	return usage
}

// emitAnthropicUsage sends what the request was billed for, before any error
// or the end of the stream, so a request that fails part-way still counts.
func emitAnthropicUsage(ch chan<- domain.ProviderEvent, usage *domain.Usage) {
	if usage == nil {
		return
	}
	reported := *usage
	ch <- domain.ProviderEvent{Usage: &reported}
}

// textRefsOnly keeps the text written before a fallback boundary, which the
// fallback model continues from; earlier thinking and tool_use blocks are
// discarded.
func textRefsOnly(layout []map[string]any) []map[string]any {
	var kept []map[string]any
	for _, block := range layout {
		if block["type"] == layoutTextRefType {
			kept = append(kept, block)
		}
	}
	return kept
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
	Message      *anthropicMessage `json:"message,omitempty"` // message_start
	Usage        *anthropicUsage   `json:"usage,omitempty"`   // message_delta
}

type anthropicMessage struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// anthropicUsage is a usage report. input_tokens excludes the cache reads and
// writes counted beside it.
type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
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
	// StopDetails is only populated when StopReason is "refusal".
	StopDetails *anthropicStopDetails `json:"stop_details,omitempty"`
}

type anthropicStopDetails struct {
	Category    string `json:"category,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

func refusalError(details *anthropicStopDetails) error {
	msg := "Claude declined to respond to this request"
	if details != nil && details.Category != "" {
		msg += fmt.Sprintf(" (category: %s)", details.Category)
	}
	if details != nil && details.Explanation != "" {
		msg += ": " + details.Explanation
	}
	return errors.New(msg)
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
