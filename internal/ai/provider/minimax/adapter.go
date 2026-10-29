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

	minimaxM3ModelID = "MiniMax-M3"

	// M3.1 Flash always reasons, takes a top-level reasoning_effort, and
	// returns its thinking as reasoning_content instead of reasoning_details.
	minimaxM31FlashModelID = "MiniMax-M3.1-Flash"
	defaultReasoningEffort = "high"
)

// Adapter streams chat completions from MiniMax.
type Adapter struct {
	client  *http.Client
	baseURL string
}

type chatRequest struct {
	Model           string                  `json:"model"`
	Messages        []message               `json:"messages"`
	Stream          bool                    `json:"stream"`
	Tools           []domain.ToolDefinition `json:"tools,omitempty"`
	ReasoningSplit  bool                    `json:"reasoning_split,omitempty"`
	Thinking        *thinking               `json:"thinking,omitempty"`
	ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	StreamOptions   *streamOptions          `json:"stream_options,omitempty"`
}

// streamOptions asks for the request's usage in the stream's final chunk,
// which MiniMax leaves out by default.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatUsage is a request's usage. prompt_tokens includes the cached tokens
// counted in prompt_tokens_details.
type chatUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type message struct {
	Role             string            `json:"role"`
	Content          any               `json:"content"` // a string, or content parts with images
	ToolCalls        []domain.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
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
	// Prices are USD per million tokens from
	// https://platform.minimax.io/docs/guides/pricing (October 2026), at the
	// standard service tier devpad requests. A prompt over 512K tokens is
	// billed at M3's long-context prices. Automatic caching charges nothing
	// to write the cache.
	return []domain.Model{
		{
			ID:         minimaxM31FlashModelID,
			Name:       "MiniMax M3.1 Flash",
			ProviderID: providerID,
			Vision:     true,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       false,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
				DefaultEffort:    defaultReasoningEffort,
			},
			Pricing: &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 0.15, Output: 0.60, CacheRead: 0.015}},
		},
		{
			ID:         minimaxM3ModelID,
			Name:       "MiniMax M3",
			ProviderID: providerID,
			Vision:     true,
			Thinking: domain.ThinkingCapability{
				Supported:        true,
				EnabledByDefault: true,
				CanDisable:       true,
			},
			Pricing: &domain.Pricing{
				TokenPrices:          domain.TokenPrices{Input: 0.30, Output: 1.20, CacheRead: 0.06},
				LongContextThreshold: 512_000,
				LongContext:          &domain.TokenPrices{Input: 0.60, Output: 2.40, CacheRead: 0.12},
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
	// Images tools returned go in a user message after the run of tool
	// messages, which must directly follow the assistant's tool calls.
	var toolImages []domain.ToolResultPart
	flushToolImages := func() {
		if content, ok := shared.ToolImagesContent(toolImages, shared.ImageURLObject); ok {
			messages = append(messages, message{Role: "user", Content: content})
		}
		toolImages = nil
	}

	for _, turn := range req.Turns {
		if turn.Role == domain.RoleUser {
			toolResults := turn.ToolResults()
			if len(toolResults) > 0 {
				toolImages = append(toolImages, toolResults...)
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

		flushToolImages()
		minimaxMsg := message{
			Role:      string(turn.Role),
			Content:   shared.ChatContent(turn.Text(), turn.Images(), shared.ImageURLObject),
			ToolCalls: turn.ToolCalls(),
		}

		if thinkingEnabled && turn.Role == domain.RoleAssistant {
			if usesReasoningContent(model) {
				minimaxMsg.ReasoningContent = turn.ThinkingText()
			} else {
				minimaxMsg.ReasoningDetails = reasoningDetailsFromTurn(turn)
			}
		}

		messages = append(messages, minimaxMsg)
	}
	flushToolImages()

	body := chatRequest{
		Model:         req.Model,
		Messages:      messages,
		Stream:        true,
		Tools:         req.Tools,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if thinkingEnabled {
		body.ReasoningSplit = true
	}
	if thinking := thinkingConfig(model, thinkingEnabled); thinking != nil {
		body.Thinking = thinking
	}
	if usesReasoningContent(model) {
		body.ReasoningEffort = requestedReasoningEffort(model, req.Thinking)
	}

	return body
}

// usesReasoningContent reports whether the model exchanges reasoning through
// reasoning_content and accepts reasoning_effort, rather than reasoning_details.
func usesReasoningContent(model domain.Model) bool {
	return strings.EqualFold(model.ID, minimaxM31FlashModelID)
}

func requestedReasoningEffort(model domain.Model, thinkingCfg *domain.ThinkingConfig) string {
	if thinkingCfg != nil {
		if effort := strings.TrimSpace(thinkingCfg.Effort); effort != "" {
			return effort
		}
	}
	if model.Thinking.DefaultEffort != "" {
		return model.Thinking.DefaultEffort
	}

	return defaultReasoningEffort
}

func thinkingConfig(model domain.Model, thinkingEnabled bool) *thinking {
	if !model.Thinking.Supported ||
		(!strings.EqualFold(model.ID, minimaxM3ModelID) && !strings.EqualFold(model.ID, minimaxM31FlashModelID)) {
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
	var usage *chatUsage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			emitUsage(ch, usage)
			emitToolCalls(ch, toolCalls, toolCallArgs)
			ch <- domain.ProviderEvent{Done: true}
			return
		}

		var chunk struct {
			Usage   *chatUsage `json:"usage"`
			Choices []struct {
				Delta struct {
					Content          string            `json:"content"`
					ReasoningDetails []reasoningDetail `json:"reasoning_details"`
					ReasoningContent string            `json:"reasoning_content"`
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
		if chunk.Usage != nil {
			usage = chunk.Usage
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
		} else if choice.Delta.ReasoningContent != "" {
			// reasoning_content arrives as incremental fragments, unlike the
			// cumulative reasoning_details payloads.
			ch <- domain.ProviderEvent{ReasoningDelta: choice.Delta.ReasoningContent}
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

	emitUsage(ch, usage)
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

// emitUsage sends what the request was billed for, before any error or the
// end of the stream. Cached prompt tokens are billed at their own rate.
func emitUsage(ch chan<- domain.ProviderEvent, usage *chatUsage) {
	if usage == nil {
		return
	}
	cached := min(usage.PromptTokensDetails.CachedTokens, usage.PromptTokens)
	ch <- domain.ProviderEvent{Usage: &domain.Usage{
		InputTokens:     usage.PromptTokens - cached,
		OutputTokens:    usage.CompletionTokens,
		CacheReadTokens: cached,
	}}
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
