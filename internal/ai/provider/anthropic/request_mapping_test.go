package anthropic

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func TestBuildAnthropicRequest_SystemPromptMapping(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "You are a helpful assistant."),
			domain.NewTextTurn(domain.RoleUser, "Hello"),
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	if got := systemText(t, body); got != "You are a helpful assistant." {
		t.Fatalf("expected system prompt to be extracted, got %v", got)
	}

	messages := body["messages"].([]map[string]any)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message after system extraction, got %d", len(messages))
	}
	if messages[0]["role"] != "user" {
		t.Fatalf("expected user message, got %v", messages[0]["role"])
	}
}

func TestBuildAnthropicRequest_MultipleSystemPrompts(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "First system prompt."),
			domain.NewTextTurn(domain.RoleSystem, "Second system prompt."),
			domain.NewTextTurn(domain.RoleUser, "Hello"),
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	expected := "First system prompt.\n\nSecond system prompt."
	if got := systemText(t, body); got != expected {
		t.Fatalf("expected combined system prompt, got %v", got)
	}
}

func TestBuildAnthropicRequest_PromptCaching(t *testing.T) {
	ephemeral := map[string]any{"type": "ephemeral"}

	t.Run("with system prompt", func(t *testing.T) {
		req := aiprovider.StreamRequest{
			Model: "claude-sonnet-5-5",
			Turns: []domain.Turn{
				domain.NewTextTurn(domain.RoleSystem, "You are a helpful assistant."),
				domain.NewTextTurn(domain.RoleUser, "Hello"),
			},
		}

		body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

		if !reflect.DeepEqual(body["cache_control"], ephemeral) {
			t.Fatalf("expected automatic caching, got %v", body["cache_control"])
		}
		system := body["system"].([]map[string]any)
		if !reflect.DeepEqual(system[len(system)-1]["cache_control"], ephemeral) {
			t.Fatalf("expected a breakpoint on the last system block, got %v", system[len(system)-1]["cache_control"])
		}
	})

	t.Run("without system prompt", func(t *testing.T) {
		req := aiprovider.StreamRequest{
			Model: "claude-sonnet-5-5",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
		}

		body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

		if !reflect.DeepEqual(body["cache_control"], ephemeral) {
			t.Fatalf("expected automatic caching, got %v", body["cache_control"])
		}
		if _, ok := body["system"]; ok {
			t.Fatalf("expected no system blocks, got %v", body["system"])
		}
	})
}

// systemText returns the text of a request's system blocks.
func systemText(t *testing.T, body map[string]any) string {
	t.Helper()
	blocks, ok := body["system"].([]map[string]any)
	if !ok {
		t.Fatalf("expected system blocks, got %T", body["system"])
	}
	var text string
	for _, block := range blocks {
		if block["type"] != "text" {
			t.Fatalf("expected a text block, got %v", block["type"])
		}
		text += block["text"].(string)
	}
	return text
}

func TestBuildAnthropicRequest_ToolDefinitions(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
		Tools: []domain.ToolDefinition{
			{
				Type: "function",
				Function: domain.ToolFunction{
					Name:        "get_weather",
					Description: "Get current weather",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
				},
			},
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	tools := body["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool["name"] != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %v", tool["name"])
	}
	if tool["description"] != "Get current weather" {
		t.Fatalf("expected tool description, got %v", tool["description"])
	}

	// Verify input_schema is the parsed JSON
	schema, ok := tool["input_schema"].(json.RawMessage)
	if !ok {
		t.Fatalf("expected input_schema to be json.RawMessage, got %T", tool["input_schema"])
	}

	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("failed to parse input_schema: %v", err)
	}
	if parsed["type"] != "object" {
		t.Fatalf("expected schema type object, got %v", parsed["type"])
	}
}

func TestBuildAnthropicRequest_ToolCallInAssistantMessage(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "What's the weather?"),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{{
					Kind: domain.PartToolCall,
					ToolCall: &domain.ToolCall{
						ID:   "toolu_123",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				}},
			},
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	messages := body["messages"].([]map[string]any)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	assistantMsg := messages[1]
	content := assistantMsg["content"].([]map[string]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}

	toolUse := content[0]
	if toolUse["type"] != "tool_use" {
		t.Fatalf("expected tool_use type, got %v", toolUse["type"])
	}
	if toolUse["id"] != "toolu_123" {
		t.Fatalf("expected tool_use id toolu_123, got %v", toolUse["id"])
	}
	if toolUse["name"] != "get_weather" {
		t.Fatalf("expected tool_use name get_weather, got %v", toolUse["name"])
	}

	// Verify input is parsed JSON, not string
	input, ok := toolUse["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected input to be parsed JSON object, got %T: %v", toolUse["input"], toolUse["input"])
	}
	if input["location"] != "NYC" {
		t.Fatalf("expected location NYC, got %v", input["location"])
	}
}

func TestBuildAnthropicRequest_ToolResultMessage(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleUser, "What's the weather?"),
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{{
					Kind: domain.PartToolCall,
					ToolCall: &domain.ToolCall{
						ID:   "toolu_123",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				}},
			},
			domain.NewToolResultTurn("toolu_123", "get_weather", "Temperature is 72F", false),
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	messages := body["messages"].([]map[string]any)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	toolResultMsg := messages[2]
	if toolResultMsg["role"] != "user" {
		t.Fatalf("expected Anthropic tool result role to be normalized to user, got %v", toolResultMsg["role"])
	}

	content := toolResultMsg["content"].([]map[string]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}

	toolResult := content[0]
	if toolResult["type"] != "tool_result" {
		t.Fatalf("expected tool_result type, got %v", toolResult["type"])
	}
	if toolResult["tool_use_id"] != "toolu_123" {
		t.Fatalf("expected tool_use_id toolu_123, got %v", toolResult["tool_use_id"])
	}
	if toolResult["content"] != "Temperature is 72F" {
		t.Fatalf("expected result content, got %v", toolResult["content"])
	}
}

var claude55AdaptiveThinking = map[string]any{
	"type":          "adaptive",
	"display":       "summarized",
	"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
}

func TestBuildAnthropicRequest_Claude55Thinking(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name         string
		model        string
		thinking     *domain.ThinkingConfig
		wantThinking map[string]any
		wantEffort   string
		noFallbacks  bool
	}{
		{
			name:         "opus default",
			model:        "claude-opus-5-5",
			wantThinking: claude55AdaptiveThinking,
			wantEffort:   "medium",
		},
		{
			name:         "opus selected effort",
			model:        "claude-opus-5-5",
			thinking:     &domain.ThinkingConfig{Enabled: &enabled, Effort: "xhigh"},
			wantThinking: claude55AdaptiveThinking,
			wantEffort:   "xhigh",
		},
		{
			name:         "sonnet default",
			model:        "claude-sonnet-5-5",
			wantThinking: claude55AdaptiveThinking,
			wantEffort:   "medium",
		},
		{
			name:         "sonnet enabled without effort",
			model:        "claude-sonnet-5-5",
			thinking:     &domain.ThinkingConfig{Enabled: &enabled},
			wantThinking: claude55AdaptiveThinking,
			wantEffort:   "medium",
		},
		{
			name:         "sonnet disabled uses between_tools",
			model:        "claude-sonnet-5-5",
			thinking:     &domain.ThinkingConfig{Enabled: &disabled},
			wantThinking: map[string]any{"type": "between_tools"},
		},
		{
			name:  "opus never sends disabled",
			model: "claude-opus-5-5",
			// Rejected by domain validation; the adapter still must not send
			// a payload the API would reject.
			thinking:   &domain.ThinkingConfig{Enabled: &disabled},
			wantEffort: "",
		},
		{
			name:         "haiku default",
			model:        "claude-haiku-5-5",
			wantThinking: claude55AdaptiveThinking,
			wantEffort:   "medium",
			noFallbacks:  true,
		},
		{
			name:         "haiku disabled",
			model:        "claude-haiku-5-5",
			thinking:     &domain.ThinkingConfig{Enabled: &disabled},
			wantThinking: map[string]any{"type": "disabled"},
			noFallbacks:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := aiprovider.StreamRequest{
				Model:    tt.model,
				Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
				Thinking: tt.thinking,
			}

			body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

			thinking, _ := body["thinking"].(map[string]any)
			if !reflect.DeepEqual(thinking, tt.wantThinking) {
				t.Fatalf("expected thinking %v, got %v", tt.wantThinking, body["thinking"])
			}
			outputConfig, _ := body["output_config"].(map[string]any)
			if got, _ := outputConfig["effort"].(string); got != tt.wantEffort {
				t.Fatalf("expected effort %q, got %v", tt.wantEffort, body["output_config"])
			}
			if fallbacks, ok := body["fallbacks"]; tt.noFallbacks && ok {
				t.Fatalf("expected no fallbacks, got %v", fallbacks)
			} else if !tt.noFallbacks && fallbacks != "default" {
				t.Fatalf("expected fallbacks default, got %v", fallbacks)
			}
			if body["max_tokens"] != maxTokens {
				t.Fatalf("expected max_tokens %d, got %v", maxTokens, body["max_tokens"])
			}
		})
	}
}

func TestBuildAnthropicRequest_MixedContentMessage(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-opus-5-5",
		Turns: []domain.Turn{
			{
				Role: domain.RoleAssistant,
				Parts: []domain.Part{
					{Kind: domain.PartText, Text: "Let me check that."},
					{Kind: domain.PartToolCall, ToolCall: &domain.ToolCall{
						ID:   "toolu_123",
						Type: "function",
						Function: domain.ToolCallFunction{
							Name:      "search",
							Arguments: `{"query":"test"}`,
						},
					}},
				},
			},
		},
	}

	body := buildAnthropicRequest(req, mustAnthropicModel(t, req.Model))

	messages := body["messages"].([]map[string]any)
	assistantMsg := messages[0]
	content := assistantMsg["content"].([]map[string]any)

	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks (text + tool_use), got %d", len(content))
	}

	if content[0]["type"] != "text" {
		t.Fatalf("expected first block to be text, got %v", content[0]["type"])
	}
	if content[0]["text"] != "Let me check that." {
		t.Fatalf("expected text content, got %v", content[0]["text"])
	}

	if content[1]["type"] != "tool_use" {
		t.Fatalf("expected second block to be tool_use, got %v", content[1]["type"])
	}
}

func mustAnthropicModel(t *testing.T, modelID string) domain.Model {
	t.Helper()

	model, ok := domain.ModelByID(NewAdapter().Models(), modelID)
	if !ok {
		t.Fatalf("expected Anthropic model %q to exist", modelID)
	}
	return model
}
