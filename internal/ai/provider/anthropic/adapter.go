package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/provider/shared"
)

const (
	providerID   = "anthropic"
	providerName = "Anthropic"
	baseURL      = "https://api.anthropic.com/v1"
	apiVersion   = "2023-06-01"
	maxTokens    = 16000

	// Claude 5.5 models think by default, and thinking counts toward max_tokens,
	// so they get more room than the default to avoid truncated replies.
	claude55MaxTokens = 64000

	// serverSideFallbackBeta enables `fallbacks: "default"`, which re-runs a
	// request declined by a safety classifier on Anthropic's recommended model.
	serverSideFallbackBeta = "server-side-fallback-2026-07-01"

	// thinkingBindingBeta allows `thinking.block_binding`, which tells the API to
	// drop thinking blocks invalidated by a history edit instead of rejecting
	// the request.
	thinkingBindingBeta = "thinking-binding-controls-2026-08-01"
)

// Adapter streams chat completions from Anthropic's Messages API.
type Adapter struct {
	client  *http.Client
	baseURL string
}

// NewAdapter creates a new Anthropic Messages API adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		client:  shared.NewStreamingClient(),
		baseURL: baseURL,
	}
}

func (a *Adapter) ProviderID() string   { return providerID }
func (a *Adapter) ProviderName() string { return providerName }

func (a *Adapter) Protocol() aiprovider.Protocol {
	return aiprovider.ProtocolAnthropic
}

func (a *Adapter) Models() []domain.Model {
	// Opus 5.5 always thinks: disabling it is rejected by the API, so effort is
	// the only control. The API defaults to medium effort.
	opus55Thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: true,
		CanDisable:       false,
		SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:    "medium",
	}
	// Sonnet 5.5 thinks by default and is turned off with `between_tools`
	// rather than `disabled`. Medium is the recommended starting effort for
	// agentic coding.
	sonnet55Thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: true,
		CanDisable:       true,
		SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:    "medium",
	}
	opus47Thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: false,
		CanDisable:       true,
		SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:    "xhigh",
	}
	sonnet46Thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: false,
		CanDisable:       true,
		SupportedEfforts: []string{"low", "medium", "high", "max"},
		DefaultEffort:    "medium",
	}
	// Haiku 5.5 thinks by default and accepts `disabled` at effort high or
	// below. The API defaults to medium effort.
	haiku55Thinking := domain.ThinkingCapability{
		Supported:        true,
		EnabledByDefault: true,
		CanDisable:       true,
		SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:    "medium",
	}

	// Prices are USD per million tokens from
	// https://platform.claude.com/docs/en/about-claude/pricing (October 2026).
	// Cache writes are priced at the 5-minute rate. Only Haiku 5.5 charges
	// more for long prompts.
	models := []domain.Model{
		{
			ID:         "claude-opus-5-5",
			Name:       "Claude Opus 5.5",
			ProviderID: providerID,
			Vision:     true,
			Thinking:   opus55Thinking,
			Pricing:    &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 5}},
		},
		{
			ID:         "claude-sonnet-5-5",
			Name:       "Claude Sonnet 5.5",
			ProviderID: providerID,
			Vision:     true,
			Thinking:   sonnet55Thinking,
			Pricing:    &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 2, Output: 10, CacheRead: 0.10, CacheWrite: 2.50}},
		},
		{
			ID:         "claude-opus-4-7",
			Name:       "Claude Opus 4.7",
			ProviderID: providerID,
			Vision:     true,
			Thinking:   opus47Thinking,
			Pricing:    &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25}},
		},
		{
			ID:         "claude-sonnet-4-6",
			Name:       "Claude Sonnet 4.6",
			ProviderID: providerID,
			Vision:     true,
			Thinking:   sonnet46Thinking,
			Pricing:    &domain.Pricing{TokenPrices: domain.TokenPrices{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75}},
		},
		{
			ID:         "claude-haiku-5-5",
			Name:       "Claude Haiku 5.5",
			ProviderID: providerID,
			Vision:     true,
			Thinking:   haiku55Thinking,
			Pricing: &domain.Pricing{
				TokenPrices:          domain.TokenPrices{Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125},
				LongContextThreshold: 100_000,
				LongContext:          &domain.TokenPrices{Input: 0.50, Output: 2.50, CacheRead: 0.05, CacheWrite: 0.625},
			},
		},
	}

	// A model with server-side fallbacks can have a request run on another
	// model, billed at that model's prices. Anthropic does not publish where
	// each model falls back to, so these models get the prices of every
	// model devpad knows, the fallback targets it does not offer among them.
	fallbackPricing := map[string]domain.Pricing{
		"claude-opus-5":   {TokenPrices: domain.TokenPrices{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25}},
		"claude-opus-4-8": {TokenPrices: domain.TokenPrices{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25}},
	}
	for _, model := range models {
		fallbackPricing[model.ID] = *model.Pricing
	}
	for i := range models {
		if usesServerSideFallback(models[i].ID) {
			models[i].FallbackPricing = fallbackPricing
		}
	}
	return models
}

func (a *Adapter) Stream(ctx context.Context, creds aiprovider.Credentials, req aiprovider.StreamRequest) (<-chan domain.ProviderEvent, error) {
	model, _ := domain.ModelByID(a.Models(), req.Model)
	body := buildAnthropicRequest(req, model)
	return startAnthropicStream(ctx, a.client, a.baseURL+"/messages", creds.APIKey, body, requestBetas(body))
}

// buildAnthropicRequest converts the provider StreamRequest into an Anthropic-specific payload.
func buildAnthropicRequest(req aiprovider.StreamRequest, model domain.Model) map[string]any {
	var systemPrompt string
	var messages []map[string]any

	for _, turn := range req.Turns {
		if turn.Role == domain.RoleSystem {
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += turn.Text()
			continue
		}

		content, ok := contentFromLayout(turn)
		if !ok {
			content = contentFromParts(turn)
		}

		if len(content) == 0 {
			continue
		}

		anthropicMsg := map[string]any{"role": string(turn.Role)}
		// Simplify to plain string when there is exactly one text block.
		if len(content) == 1 && content[0]["type"] == "text" {
			anthropicMsg["content"] = content[0]["text"]
		} else {
			anthropicMsg["content"] = content
		}
		messages = append(messages, anthropicMsg)
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokensFor(req.Model),
		"stream":     true,
		// Automatic caching puts a breakpoint on the last block and moves it
		// forward as the conversation grows, so each request in an agent loop
		// reads the transcript the previous one wrote.
		"cache_control": ephemeralCache(),
	}
	if usesServerSideFallback(req.Model) {
		body["fallbacks"] = "default"
	}

	if systemPrompt != "" {
		// Tools render before the system prompt, so this breakpoint caches
		// both, and the entry survives whatever happens later in messages.
		body["system"] = []map[string]any{{
			"type":          "text",
			"text":          systemPrompt,
			"cache_control": ephemeralCache(),
		}}
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = map[string]any{
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": tool.Function.Parameters,
			}
		}
		body["tools"] = tools
	}

	if thinking := buildThinkingConfig(req, model); thinking != nil {
		body["thinking"] = thinking
	}
	if effort := requestedEffort(model, req.Thinking); effort != "" {
		body["output_config"] = map[string]any{"effort": effort}
	}

	return body
}

// contentFromLayout rebuilds an assistant turn's content blocks in the order
// the model produced them, using the layout the decoder recorded alongside the
// thinking blocks. Models that bind thinking blocks to the conversation reject
// or drop them when an earlier turn comes back reordered or merged. It reports
// false when the turn has no layout or the layout no longer matches the turn.
func contentFromLayout(turn domain.Turn) ([]map[string]any, bool) {
	if turn.Role != domain.RoleAssistant {
		return nil, false
	}
	state := turn.ThinkingState()
	if len(state) == 0 {
		return nil, false
	}
	var blocks []map[string]any
	if err := json.Unmarshal(state, &blocks); err != nil || !hasLayoutRefs(blocks) {
		return nil, false
	}

	toolCalls := make(map[string]domain.ToolCall)
	for _, part := range turn.Parts {
		if part.Kind == domain.PartToolCall && part.ToolCall != nil {
			toolCalls[part.ToolCall.ID] = *part.ToolCall
		}
	}

	remaining := turn.Text()
	used := make(map[string]bool)
	var content []map[string]any
	for _, block := range blocks {
		switch block["type"] {
		case "thinking", "redacted_thinking":
			content = append(content, block)
		case layoutTextRefType:
			n, ok := block["bytes"].(float64)
			if !ok || n < 0 || int(n) > len(remaining) {
				return nil, false
			}
			segment := remaining[:int(n)]
			remaining = remaining[int(n):]
			if segment != "" {
				content = append(content, map[string]any{"type": "text", "text": segment})
			}
		case layoutToolUseRefType:
			id, _ := block["id"].(string)
			call, ok := toolCalls[id]
			if !ok || used[id] {
				// Tool calls that never got a result are removed from the
				// turn before it is saved, so skip them here too.
				continue
			}
			used[id] = true
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Function.Name,
				"input": rawToolInput(call.Function.Arguments),
			})
		default:
			return nil, false
		}
	}
	if remaining != "" || len(used) != len(toolCalls) {
		return nil, false
	}
	return content, true
}

func hasLayoutRefs(blocks []map[string]any) bool {
	for _, block := range blocks {
		if block["type"] == layoutTextRefType || block["type"] == layoutToolUseRefType {
			return true
		}
	}
	return false
}

// rawToolInput keeps the model's own JSON bytes so key order survives replay.
func rawToolInput(arguments string) any {
	if arguments != "" && json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	return map[string]any{}
}

// contentFromParts builds content blocks from the turn's parts, with any
// thinking blocks placed where the thinking part sits.
func contentFromParts(turn domain.Turn) []map[string]any {
	var content []map[string]any

	for _, part := range turn.Parts {
		switch part.Kind {
		case domain.PartThinking:
			// Re-inject prior thinking blocks verbatim for multi-turn continuity.
			if part.Thinking != nil && len(part.Thinking.State) > 0 {
				var blocks []map[string]any
				if err := json.Unmarshal(part.Thinking.State, &blocks); err == nil {
					for _, block := range blocks {
						if block["type"] == "thinking" || block["type"] == "redacted_thinking" {
							content = append(content, block)
						}
					}
				}
			}
		case domain.PartText:
			if part.Text != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": part.Text,
				})
			}
		case domain.PartToolCall:
			if part.ToolCall == nil {
				continue
			}
			var input any
			if part.ToolCall.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(part.ToolCall.Function.Arguments), &input); err != nil {
					input = map[string]any{}
				}
			} else {
				input = map[string]any{}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    part.ToolCall.ID,
				"name":  part.ToolCall.Function.Name,
				"input": input,
			})
		case domain.PartToolResult:
			if part.ToolResult == nil || part.ToolResult.ToolCallID == "" {
				continue
			}
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": part.ToolResult.ToolCallID,
				"content":     part.ToolResult.Content,
			}
			if len(part.ToolResult.Images) > 0 {
				blocks := make([]map[string]any, 0, len(part.ToolResult.Images)+1)
				if part.ToolResult.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": part.ToolResult.Content})
				}
				for _, image := range part.ToolResult.Images {
					blocks = append(blocks, imageBlock(image))
				}
				block["content"] = blocks
			}
			content = append(content, block)
		case domain.PartImage:
			if part.Image != nil {
				content = append(content, imageBlock(*part.Image))
			}
		}
	}

	return content
}

func imageBlock(image domain.ImagePart) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": image.MediaType,
			"data":       base64.StdEncoding.EncodeToString(image.Data),
		},
	}
}

func buildThinkingConfig(req aiprovider.StreamRequest, model domain.Model) map[string]any {
	if !model.Thinking.Supported {
		return nil
	}

	request := domain.ChatRequest{
		Model:    req.Model,
		Turns:    req.Turns,
		Thinking: req.Thinking,
	}
	if domain.ThinkingEnabledForRequest(model, request) {
		if isClaude55(model.ID) {
			// These models return empty thinking blocks by default, which would
			// hide both their reasoning and the progress notes they now write
			// between tool calls. If an earlier turn no longer matches what the
			// model produced, drop the stale thinking rather than fail.
			return map[string]any{
				"type":          "adaptive",
				"display":       "summarized",
				"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
			}
		}
		return map[string]any{"type": "adaptive"}
	}

	if req.Thinking != nil && req.Thinking.Enabled != nil && !*req.Thinking.Enabled && model.Thinking.CanDisable {
		if model.ID == "claude-sonnet-5-5" {
			// `disabled` is rejected on Sonnet 5.5. `between_tools` is its
			// lowest setting and is only valid at effort high or below, which
			// holds because disabled requests never carry an effort.
			return map[string]any{"type": "between_tools"}
		}
		return map[string]any{"type": "disabled"}
	}

	return nil
}

// ephemeralCache is a cache breakpoint with the default 5-minute TTL, which
// every request that reads the entry refreshes.
func ephemeralCache() map[string]any {
	return map[string]any{"type": "ephemeral"}
}

func isClaude55(modelID string) bool {
	switch modelID {
	case "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5":
		return true
	default:
		return false
	}
}

// usesServerSideFallback reports whether a model takes `fallbacks: "default"`.
// Haiku 5.5 has no server-side fallback: a declined request stays declined.
func usesServerSideFallback(modelID string) bool {
	return modelID == "claude-opus-5-5" || modelID == "claude-sonnet-5-5"
}

func maxTokensFor(modelID string) int {
	if isClaude55(modelID) {
		return claude55MaxTokens
	}
	return maxTokens
}

func requestBetas(body map[string]any) []string {
	var betas []string
	if _, ok := body["fallbacks"]; ok {
		betas = append(betas, serverSideFallbackBeta)
	}
	if thinking, ok := body["thinking"].(map[string]any); ok {
		if _, ok := thinking["block_binding"]; ok {
			betas = append(betas, thinkingBindingBeta)
		}
	}
	return betas
}

func requestedEffort(model domain.Model, thinking *domain.ThinkingConfig) string {
	if !model.Thinking.SupportsEffortSelection() {
		return ""
	}
	if thinking != nil && thinking.Effort != "" {
		return thinking.Effort
	}
	if model.Thinking.DefaultEffort == "" {
		return ""
	}
	if thinking != nil && thinking.Enabled != nil {
		if *thinking.Enabled {
			return model.Thinking.DefaultEffort
		}
		return ""
	}
	// Models that think by default use the app's default effort rather than
	// the API's, which differs per model.
	if model.Thinking.EnabledByDefault {
		return model.Thinking.DefaultEffort
	}
	return ""
}
