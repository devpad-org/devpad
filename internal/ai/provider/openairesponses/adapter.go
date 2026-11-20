package openairesponses

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "openai"
	providerName = "OpenAI"
	baseURL      = "https://api.openai.com/v1"

	defaultReasoningEffort  = "medium"
	disabledReasoningEffort = "none"
)

// Adapter streams chat responses from OpenAI's Responses API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

type streamEvent struct {
	Type         string           `json:"type"`
	ItemID       string           `json:"item_id,omitempty"`
	OutputIndex  int              `json:"output_index,omitempty"`
	ContentIndex int              `json:"content_index,omitempty"`
	SummaryIndex int              `json:"summary_index,omitempty"`
	Delta        string           `json:"delta,omitempty"`
	Text         string           `json:"text,omitempty"`
	Name         string           `json:"name,omitempty"`
	Arguments    string           `json:"arguments,omitempty"`
	Message      string           `json:"message,omitempty"`
	Item         json.RawMessage  `json:"item,omitempty"`
	Response     *responsePayload `json:"response,omitempty"`
}

type responsePayload struct {
	Error             *responseError     `json:"error,omitempty"`
	IncompleteDetails *incompleteDetails `json:"incomplete_details,omitempty"`
	Output            []json.RawMessage  `json:"output,omitempty"`
	Usage             *responseUsage     `json:"usage,omitempty"`
}

// responseUsage is a response's usage. input_tokens includes the tokens read
// from and written to the cache counted in input_tokens_details.
type responseUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type responseError struct {
	Message string `json:"message,omitempty"`
}

type incompleteDetails struct {
	Reason string `json:"reason,omitempty"`
}

type outputItem struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Status    string `json:"status,omitempty"`
}

type pendingToolCall struct {
	itemID      string
	outputIndex int
	call        domain.ToolCall
	args        strings.Builder
}

// NewAdapter creates a new OpenAI Responses adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		client:  shared.NewStreamingClient(),
		baseURL: baseURL,
	}
}

func (a *Adapter) ProviderID() string   { return providerID }
func (a *Adapter) ProviderName() string { return providerName }

func (a *Adapter) Protocol() aiprovider.Protocol {
	return aiprovider.ProtocolOpenAIResponses
}

func (a *Adapter) Models() []domain.Model {
	// GPT-6.2 models always reason: the API rejects the effort "none".
	alwaysReasons := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: true,
		CanDisable:       false,
		SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:    defaultReasoningEffort,
	}
	reasonsByDefault := alwaysReasons
	reasonsByDefault.CanDisable = true

	// Prices are USD per million tokens from
	// https://developers.openai.com/api/docs/pricing (November 2026). A prompt
	// over 272K tokens is billed at the long-context prices. CacheWrite is the
	// price of the default 30-minute cache lifetime, the only one devpad uses.
	astraPricing := &domain.Pricing{
		TokenPrices:          domain.TokenPrices{Input: 8, Output: 40, CacheRead: 0.40, CacheWrite: 10},
		LongContextThreshold: 272_000,
		LongContext:          &domain.TokenPrices{Input: 16, Output: 60, CacheRead: 0.80, CacheWrite: 20},
	}
	solPricing := &domain.Pricing{
		TokenPrices:          domain.TokenPrices{Input: 2, Output: 10, CacheRead: 0.10, CacheWrite: 2.50},
		LongContextThreshold: 272_000,
		LongContext:          &domain.TokenPrices{Input: 4, Output: 15, CacheRead: 0.20, CacheWrite: 5},
	}
	terraPricing := &domain.Pricing{
		TokenPrices:          domain.TokenPrices{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25},
		LongContextThreshold: 272_000,
		LongContext:          &domain.TokenPrices{Input: 2, Output: 7.50, CacheRead: 0.10, CacheWrite: 2.50},
	}
	lunaPricing := &domain.Pricing{
		TokenPrices:          domain.TokenPrices{Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125},
		LongContextThreshold: 272_000,
		LongContext:          &domain.TokenPrices{Input: 0.20, Output: 0.75, CacheRead: 0.02, CacheWrite: 0.25},
	}

	// Sol comes first: the IDE starts on the first model when nothing else
	// picks one, and Astra costs four times as much.
	return []domain.Model{
		{ID: "gpt-6.2-sol", Name: "GPT-6.2 Sol", ProviderID: providerID, Thinking: alwaysReasons, Vision: true, Pricing: solPricing},
		{ID: "gpt-6.2-astra", Name: "GPT-6.2 Astra", ProviderID: providerID, Thinking: alwaysReasons, Vision: true, Pricing: astraPricing},
		{ID: "gpt-6.1-terra", Name: "GPT-6.1 Terra", ProviderID: providerID, Thinking: reasonsByDefault, Vision: true, Pricing: terraPricing},
		{ID: "gpt-6-luna", Name: "GPT-6 Luna", ProviderID: providerID, Thinking: reasonsByDefault, Vision: true, Pricing: lunaPricing},
	}
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	model, _ := domain.ModelByID(a.Models(), req.Model)
	body := buildResponsesRequest(req, model)

	return shared.StartStreamRequest(ctx, a.client, a.baseURL+"/responses", creds.APIKey, providerID, body, readResponsesStream)
}

func buildResponsesRequest(req aiprovider.StreamRequest, model domain.Model) map[string]any {
	body := map[string]any{
		"model":  req.Model,
		"stream": true,
		"store":  false,
	}

	if input := buildInput(req.Turns); len(input) > 0 {
		body["input"] = input
	}

	if len(req.Tools) > 0 {
		body["tools"] = buildTools(req.Tools)
		body["tool_choice"] = "auto"
	}

	if reasoning := buildReasoningConfig(req, model); reasoning != nil {
		body["reasoning"] = reasoning
	}

	if include := buildInclude(req, model); len(include) > 0 {
		body["include"] = include
	}

	return body
}

// systemPrompt joins the system turns into one prompt.
func systemPrompt(turns []domain.Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != domain.RoleSystem || turn.Text() == "" {
			continue
		}
		parts = append(parts, turn.Text())
	}

	return strings.Join(parts, "\n\n")
}

// buildInput returns the request's input items. The system prompt is the first
// of them, as a developer message, and not the top-level instructions: GPT-6.2
// models read those after the input and bill them as uncached on every request.
func buildInput(turns []domain.Turn) []any {
	input := make([]any, 0, len(turns)+1)

	if prompt := systemPrompt(turns); prompt != "" {
		input = append(input, map[string]any{
			"role":    "developer",
			"content": prompt,
		})
	}

	for _, turn := range turns {
		if turn.Role == domain.RoleSystem {
			continue
		}

		appendReasoningItems(&input, turn.ThinkingState())

		switch turn.Role {
		case domain.RoleAssistant:
			if turn.Text() != "" {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": turn.Text(),
				})
			}
			for _, toolCall := range turn.ToolCalls() {
				if toolCall.ID == "" {
					continue
				}
				item := map[string]any{
					"type":      "function_call",
					"call_id":   toolCall.ID,
					"name":      toolCall.Function.Name,
					"arguments": toolCall.Function.Arguments,
					"status":    "completed",
				}
				if toolCall.ItemID != "" {
					item["id"] = toolCall.ItemID
				}
				input = append(input, item)
			}
		case domain.RoleUser:
			toolResults := turn.ToolResults()
			if len(toolResults) > 0 {
				for _, result := range toolResults {
					if result.ToolCallID == "" {
						continue
					}
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": result.ToolCallID,
						"output":  toolOutput(result),
					})
				}
				if text := turn.Text(); text != "" {
					input = append(input, map[string]any{
						"role":    "user",
						"content": text,
					})
				}
			} else if images := turn.Images(); len(images) > 0 {
				content := make([]map[string]any, 0, len(images)+1)
				for _, image := range images {
					content = append(content, inputImage(image))
				}
				if text := turn.Text(); text != "" {
					content = append(content, map[string]any{"type": "input_text", "text": text})
				}
				input = append(input, map[string]any{
					"role":    "user",
					"content": content,
				})
			} else if text := turn.Text(); text != "" {
				input = append(input, map[string]any{
					"role":    "user",
					"content": text,
				})
			}
		}
	}

	return input
}

// toolOutput returns a function call's output: its text, or with images,
// the text and images as content parts.
func toolOutput(result domain.ToolResultPart) any {
	if len(result.Images) == 0 {
		return result.Content
	}
	output := make([]map[string]any, 0, len(result.Images)+1)
	if result.Content != "" {
		output = append(output, map[string]any{"type": "input_text", "text": result.Content})
	}
	for _, image := range result.Images {
		output = append(output, inputImage(image))
	}
	return output
}

func inputImage(image domain.ImagePart) map[string]any {
	return map[string]any{
		"type":      "input_image",
		"image_url": shared.DataURL(image),
		"detail":    "auto",
	}
}

// appendReasoningItems adds a turn's reasoning items to the input. The turn may
// have come from another provider, whose state the API would refuse, so only
// reasoning items go.
func appendReasoningItems(input *[]any, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return
		}
		items = []map[string]any{item}
	}
	for _, item := range items {
		if item["type"] == "reasoning" {
			*input = append(*input, item)
		}
	}
}

func buildTools(definitions []domain.ToolDefinition) []map[string]any {
	tools := make([]map[string]any, 0, len(definitions))
	for _, tool := range definitions {
		if tool.Type != "function" {
			continue
		}

		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
			"strict":      false,
		})
	}

	return tools
}

func buildReasoningConfig(req aiprovider.StreamRequest, model domain.Model) map[string]any {
	if !model.Thinking.Supported {
		return nil
	}

	request := domain.ChatRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
	}

	if domain.ThinkingEnabledForRequest(model, request) {
		effort := requestedReasoningEffort(model, req.Thinking)
		return map[string]any{
			"effort":  effort,
			"summary": "auto",
		}
	}

	if req.Thinking != nil && req.Thinking.Enabled != nil && !*req.Thinking.Enabled && model.Thinking.CanDisable {
		return map[string]any{
			"effort": disabledReasoningEffort,
		}
	}

	return nil
}

func requestedReasoningEffort(model domain.Model, thinking *domain.ThinkingConfig) string {
	if thinking != nil {
		if effort := strings.TrimSpace(thinking.Effort); effort != "" {
			return effort
		}
	}

	if model.Thinking.SupportsEffortSelection() && model.Thinking.DefaultEffort != "" {
		return model.Thinking.DefaultEffort
	}

	return defaultReasoningEffort
}

func buildInclude(req aiprovider.StreamRequest, model domain.Model) []string {
	if !model.Thinking.Supported {
		return nil
	}

	if !domain.ThinkingEnabledForRequest(model, domain.ChatRequest{
		Model:    req.Model,
		Thinking: req.Thinking,
	}) {
		return nil
	}

	return []string{"reasoning.encrypted_content"}
}

func readResponsesStream(body io.ReadCloser, ch chan<- domain.ProviderEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	toolCallsByIndex := make(map[int]*pendingToolCall)
	toolCallsByItemID := make(map[string]*pendingToolCall)
	contentBuffers := make(map[string]string)
	reasoningBuffers := make(map[string]string)
	var reasoningItems []json.RawMessage
	var dataLines []string

	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}

		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]

		if data == "[DONE]" {
			emitResponseToolCalls(ch, toolCallsByIndex)
			ch <- domain.ProviderEvent{Done: true}
			return false
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			ch <- domain.ProviderEvent{Err: shared.MalformedEventError(providerName, data, err)}
			return false
		}

		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if event.Delta != "" {
				bufferKey := textBufferKey(event.ItemID, event.ContentIndex)
				contentBuffers[bufferKey] += event.Delta
				ch <- domain.ProviderEvent{TextDelta: event.Delta}
			}
		case "response.output_text.done", "response.refusal.done":
			bufferKey := textBufferKey(event.ItemID, event.ContentIndex)
			if delta := streamDelta(contentBuffers[bufferKey], event.Text); delta != "" {
				ch <- domain.ProviderEvent{TextDelta: delta}
			}
			contentBuffers[bufferKey] = event.Text
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if event.Delta != "" {
				bufferKey := reasoningBufferKey(event)
				reasoningBuffers[bufferKey] += event.Delta
				ch <- domain.ProviderEvent{ReasoningDelta: event.Delta}
			}
		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			bufferKey := reasoningBufferKey(event)
			if delta := streamDelta(reasoningBuffers[bufferKey], event.Text); delta != "" {
				ch <- domain.ProviderEvent{ReasoningDelta: delta}
			}
			reasoningBuffers[bufferKey] = event.Text
		case "response.output_item.added":
			registerToolCall(event, toolCallsByIndex, toolCallsByItemID)
		case "response.function_call_arguments.delta":
			appendToolCallDelta(event, toolCallsByIndex, toolCallsByItemID)
		case "response.function_call_arguments.done":
			finalizeToolCallArguments(event, toolCallsByIndex, toolCallsByItemID)
		case "response.output_item.done":
			if handleOutputItemDone(event, toolCallsByIndex, toolCallsByItemID, &reasoningItems) {
				if state := marshalReasoningState(reasoningItems); len(state) > 0 {
					ch <- domain.ProviderEvent{ReasoningState: state}
				}
			}
		case "response.completed":
			emitResponseUsage(ch, event.Response)
			captureResponseOutput(event.Response, toolCallsByIndex, toolCallsByItemID)
			if state := marshalReasoningState(reasoningItems); len(state) > 0 {
				ch <- domain.ProviderEvent{ReasoningState: state}
			}
			emitResponseToolCalls(ch, toolCallsByIndex)
			ch <- domain.ProviderEvent{Done: true}
			return false
		case "response.failed":
			emitResponseUsage(ch, event.Response)
			message := responseErrorMessage(event.Response)
			if message == "" {
				message = "OpenAI response failed"
			}
			ch <- domain.ProviderEvent{Err: errors.New(message)}
			return false
		case "response.incomplete":
			emitResponseUsage(ch, event.Response)
			message := responseIncompleteMessage(event.Response)
			if message == "" {
				message = "OpenAI response incomplete"
			}
			ch <- domain.ProviderEvent{Err: errors.New(message)}
			return false
		case "error":
			if event.Message == "" {
				event.Message = "OpenAI streaming error"
			}
			ch <- domain.ProviderEvent{Err: errors.New(event.Message)}
			return false
		}

		return true
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if !flush() {
				return
			}
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if !flush() {
		return
	}

	if err := scanner.Err(); err != nil {
		ch <- domain.ProviderEvent{Err: shared.ReadStreamError(providerName, err)}
		return
	}
	// The stream closed before response.completed, so the response is incomplete.
	ch <- domain.ProviderEvent{Err: shared.IncompleteStreamError(providerName)}
}

func registerToolCall(event streamEvent, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall) {
	var item outputItem
	if err := json.Unmarshal(event.Item, &item); err != nil || item.Type != "function_call" {
		return
	}

	pending := ensurePendingToolCall(byIndex, byItemID, event.OutputIndex, item.ID)
	pending.call.Type = "function"
	pending.call.ID = firstNonEmpty(item.CallID, item.ID)
	pending.call.Function.Name = item.Name
	pending.call.Function.Arguments = item.Arguments
	if item.Arguments != "" {
		pending.args.Reset()
		pending.args.WriteString(item.Arguments)
	}
}

func appendToolCallDelta(event streamEvent, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall) {
	pending := ensurePendingToolCall(byIndex, byItemID, event.OutputIndex, event.ItemID)
	if pending.call.Type == "" {
		pending.call.Type = "function"
	}
	if pending.call.ID == "" {
		pending.call.ID = event.ItemID
	}
	pending.args.WriteString(event.Delta)
	pending.call.Function.Arguments = pending.args.String()
}

func finalizeToolCallArguments(event streamEvent, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall) {
	pending := ensurePendingToolCall(byIndex, byItemID, event.OutputIndex, event.ItemID)
	if pending.call.Type == "" {
		pending.call.Type = "function"
	}
	if pending.call.ID == "" {
		pending.call.ID = event.ItemID
	}
	if event.Name != "" {
		pending.call.Function.Name = event.Name
	}
	if event.Arguments != "" {
		pending.args.Reset()
		pending.args.WriteString(event.Arguments)
	}
	pending.call.Function.Arguments = pending.args.String()
}

func handleOutputItemDone(event streamEvent, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall, reasoningItems *[]json.RawMessage) bool {
	var item outputItem
	if err := json.Unmarshal(event.Item, &item); err != nil {
		return false
	}

	switch item.Type {
	case "function_call":
		pending := ensurePendingToolCall(byIndex, byItemID, event.OutputIndex, item.ID)
		pending.call.Type = "function"
		pending.call.ID = firstNonEmpty(item.CallID, item.ID)
		pending.call.Function.Name = item.Name
		if item.Arguments != "" {
			pending.args.Reset()
			pending.args.WriteString(item.Arguments)
		}
		pending.call.Function.Arguments = pending.args.String()
	case "reasoning":
		*reasoningItems = append(*reasoningItems, append(json.RawMessage(nil), event.Item...))
		return true
	}

	return false
}

func captureResponseOutput(response *responsePayload, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall) {
	if response == nil {
		return
	}

	for index, raw := range response.Output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		if item.Type != "function_call" {
			continue
		}

		pending := ensurePendingToolCall(byIndex, byItemID, index, item.ID)
		pending.call.Type = "function"
		pending.call.ID = firstNonEmpty(item.CallID, item.ID)
		pending.call.Function.Name = item.Name
		pending.args.Reset()
		pending.args.WriteString(item.Arguments)
		pending.call.Function.Arguments = pending.args.String()
	}
}

func ensurePendingToolCall(byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall, outputIndex int, itemID string) *pendingToolCall {
	if pending, ok := byItemID[itemID]; ok {
		return pending
	}
	if pending, ok := byIndex[outputIndex]; ok {
		if itemID != "" {
			pending.itemID = itemID
			byItemID[itemID] = pending
		}
		return pending
	}

	pending := &pendingToolCall{
		itemID:      itemID,
		outputIndex: outputIndex,
		call: domain.ToolCall{
			Type: "function",
		},
	}
	byIndex[outputIndex] = pending
	if itemID != "" {
		byItemID[itemID] = pending
	}

	return pending
}

func emitResponseToolCalls(ch chan<- domain.ProviderEvent, byIndex map[int]*pendingToolCall) {
	if len(byIndex) == 0 {
		return
	}

	indexes := make([]int, 0, len(byIndex))
	for index := range byIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	toolCalls := make([]domain.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		pending := byIndex[index]
		if pending == nil {
			continue
		}

		call := pending.call
		if call.ID == "" {
			call.ID = pending.itemID
		}
		if call.ItemID == "" {
			call.ItemID = pending.itemID
		}
		if call.Function.Arguments == "" {
			call.Function.Arguments = pending.args.String()
		}
		if call.ID == "" || call.Function.Name == "" {
			continue
		}

		toolCalls = append(toolCalls, call)
	}

	if len(toolCalls) == 0 {
		return
	}

	ch <- domain.ProviderEvent{ToolCalls: toolCalls}
}

func marshalReasoningState(items []json.RawMessage) json.RawMessage {
	if len(items) == 0 {
		return nil
	}

	raw, err := json.Marshal(items)
	if err != nil {
		return nil
	}

	return raw
}

// emitResponseUsage sends what a finished response was billed for.
func emitResponseUsage(ch chan<- domain.ProviderEvent, response *responsePayload) {
	if response == nil || response.Usage == nil {
		return
	}
	ch <- domain.ProviderEvent{Usage: shared.ReportedUsage{
		PromptTokens:     response.Usage.InputTokens,
		CachedTokens:     response.Usage.InputTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.InputTokensDetails.CacheWriteTokens,
		OutputTokens:     response.Usage.OutputTokens,
	}.Billed()}
}

func responseErrorMessage(response *responsePayload) string {
	if response == nil || response.Error == nil {
		return ""
	}

	return response.Error.Message
}

func responseIncompleteMessage(response *responsePayload) string {
	if response == nil || response.IncompleteDetails == nil || response.IncompleteDetails.Reason == "" {
		return ""
	}

	return "OpenAI response incomplete: " + response.IncompleteDetails.Reason
}

func textBufferKey(itemID string, contentIndex int) string {
	return itemID + ":" + strconv.Itoa(contentIndex)
}

func reasoningBufferKey(event streamEvent) string {
	index := event.ContentIndex
	if strings.Contains(event.Type, "summary") {
		index = event.SummaryIndex
	}

	return event.ItemID + ":" + strconv.Itoa(index)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}
