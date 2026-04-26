package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
)

func TestBuildAnthropicRequest_SystemPromptMapping(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-3-5-sonnet-20241022",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "You are a helpful assistant."),
			domain.NewTextTurn(domain.RoleUser, "Hello"),
		},
	}

	body := buildAnthropicRequest(req)

	if body["system"] != "You are a helpful assistant." {
		t.Fatalf("expected system prompt to be extracted, got %v", body["system"])
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
		Model: "claude-3-5-sonnet-20241022",
		Turns: []domain.Turn{
			domain.NewTextTurn(domain.RoleSystem, "First system prompt."),
			domain.NewTextTurn(domain.RoleSystem, "Second system prompt."),
			domain.NewTextTurn(domain.RoleUser, "Hello"),
		},
	}

	body := buildAnthropicRequest(req)

	expected := "First system prompt.\n\nSecond system prompt."
	if body["system"] != expected {
		t.Fatalf("expected combined system prompt, got %v", body["system"])
	}
}

func TestBuildAnthropicRequest_ToolDefinitions(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-3-5-sonnet-20241022",
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

	body := buildAnthropicRequest(req)

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
		Model: "claude-3-5-sonnet-20241022",
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

	body := buildAnthropicRequest(req)

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
		Model: "claude-3-5-sonnet-20241022",
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

	body := buildAnthropicRequest(req)

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

func TestBuildAnthropicRequest_ThinkingEnabled(t *testing.T) {
	enabled := true
	req := aiprovider.StreamRequest{
		Model:    "claude-sonnet-4-20250514",
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
		Thinking: &domain.ThinkingConfig{Enabled: &enabled},
	}

	body := buildAnthropicRequest(req)

	thinking := body["thinking"].(map[string]any)
	if thinking["type"] != "enabled" {
		t.Fatalf("expected thinking type enabled, got %v", thinking["type"])
	}
	if thinking["budget_tokens"] != 10000 {
		t.Fatalf("expected budget_tokens 10000, got %v", thinking["budget_tokens"])
	}
}

func TestBuildAnthropicRequest_ThinkingDisabled(t *testing.T) {
	disabled := false
	req := aiprovider.StreamRequest{
		Model:    "claude-sonnet-4-20250514",
		Turns:    []domain.Turn{domain.NewTextTurn(domain.RoleUser, "Hello")},
		Thinking: &domain.ThinkingConfig{Enabled: &disabled},
	}

	body := buildAnthropicRequest(req)

	thinking := body["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("expected thinking type disabled, got %v", thinking["type"])
	}
}

func TestBuildAnthropicRequest_MixedContentMessage(t *testing.T) {
	req := aiprovider.StreamRequest{
		Model: "claude-3-5-sonnet-20241022",
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

	body := buildAnthropicRequest(req)

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
