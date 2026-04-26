package openairesponses

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
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

	defaultReasoningEffort = "medium"
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
	thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: false,
		CanDisable:       true,
	}

	return []domain.Model{
		{ID: "gpt-5", Name: "GPT-5", ProviderID: providerID, Thinking: thinking},
		{ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", ProviderID: providerID, Thinking: thinking},
		{ID: "gpt-5-mini", Name: "GPT-5 Mini", ProviderID: providerID, Thinking: thinking},
		{ID: "gpt-5-nano", Name: "GPT-5 Nano", ProviderID: providerID, Thinking: thinking},
		{ID: "o4-mini", Name: "o4-mini", ProviderID: providerID, Thinking: thinking},
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

	if instructions := buildInstructions(req.Turns); instructions != "" {
		body["instructions"] = instructions
	}

	if input := buildInput(req.Turns); len(input) > 0 {
		body["input"] = input
	}

	if len(req.Tools) > 0 {
		body["tools"] = buildTools(req.Tools)
		body["tool_choice"] = "auto"
		body["parallel_tool_calls"] = true
	}

	if reasoning := buildReasoningConfig(req, model); reasoning != nil {
		body["reasoning"] = reasoning
	}

	if include := buildInclude(model); len(include) > 0 {
		body["include"] = include
	}

	return body
}

func buildInstructions(turns []domain.Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != domain.RoleSystem || turn.Text() == "" {
			continue
		}
		parts = append(parts, turn.Text())
	}

	return strings.Join(parts, "\n\n")
}

func buildInput(turns []domain.Turn) []any {
	input := make([]any, 0, len(turns))

	for _, turn := range turns {
		if turn.Role == domain.RoleSystem {
			continue
		}

		appendReasoningItems(&input, turn.ReasoningState())

		switch turn.Role {
		case domain.RoleUser, domain.RoleAssistant:
			if turn.Text() != "" {
				input = append(input, map[string]any{
					"role":    string(turn.Role),
					"content": turn.Text(),
				})
			}

			for _, toolCall := range turn.ToolCalls() {
				callID := toolCall.ID
				if callID == "" {
					continue
				}

				input = append(input, map[string]any{
					"type":      "function_call",
					"id":        callID,
					"call_id":   callID,
					"name":      toolCall.Function.Name,
					"arguments": toolCall.Function.Arguments,
					"status":    "completed",
				})
			}
		case domain.RoleTool:
			toolResult := turn.ToolResult()
			if toolResult == nil || toolResult.ToolCallID == "" {
				continue
			}

			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": toolResult.ToolCallID,
				"output":  toolResult.Content,
			})
		}
	}

	return input
}

func appendReasoningItems(input *[]any, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
		for _, item := range items {
			var parsed map[string]any
			if err := json.Unmarshal(item, &parsed); err != nil {
				continue
			}
			*input = append(*input, parsed)
		}
		return
	}

	var item map[string]any
	if err := json.Unmarshal(raw, &item); err == nil && len(item) > 0 {
		*input = append(*input, item)
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

	enabled := domain.ThinkingEnabledForRequest(model, request)
	if !enabled {
		if req.Thinking == nil || req.Thinking.Enabled == nil || model.Thinking.EnabledByDefault {
			return nil
		}

		return map[string]any{"effort": "none"}
	}

	return map[string]any{
		"effort":  defaultReasoningEffort,
		"summary": "auto",
	}
}

func buildInclude(model domain.Model) []string {
	if !model.Thinking.Supported {
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
			return true
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
			captureResponseOutput(event.Response, toolCallsByIndex, toolCallsByItemID, &reasoningItems)
			if state := marshalReasoningState(reasoningItems); len(state) > 0 {
				ch <- domain.ProviderEvent{ReasoningState: state}
			}
			emitResponseToolCalls(ch, toolCallsByIndex)
			ch <- domain.ProviderEvent{Done: true}
			return false
		case "response.failed":
			message := responseErrorMessage(event.Response)
			if message == "" {
				message = "OpenAI response failed"
			}
			ch <- domain.ProviderEvent{Err: errors.New(message)}
			return false
		case "response.incomplete":
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
		log.Printf("error reading OpenAI Responses stream: %v", err)
		ch <- domain.ProviderEvent{Err: errors.New("error reading response stream")}
		return
	}

	emitResponseToolCalls(ch, toolCallsByIndex)
	ch <- domain.ProviderEvent{Done: true}
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

func captureResponseOutput(response *responsePayload, byIndex map[int]*pendingToolCall, byItemID map[string]*pendingToolCall, reasoningItems *[]json.RawMessage) {
	if response == nil {
		return
	}

	for index, raw := range response.Output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		switch item.Type {
		case "function_call":
			pending := ensurePendingToolCall(byIndex, byItemID, index, item.ID)
			pending.call.Type = "function"
			pending.call.ID = firstNonEmpty(item.CallID, item.ID)
			pending.call.Function.Name = item.Name
			pending.args.Reset()
			pending.args.WriteString(item.Arguments)
			pending.call.Function.Arguments = pending.args.String()
		case "reasoning":
			*reasoningItems = append(*reasoningItems, append(json.RawMessage(nil), raw...))
		}
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
