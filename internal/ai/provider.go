package ai

import (
	"context"
	"encoding/json"
)

// Provider is the interface that all AI chat providers must implement.
type Provider interface {
	// ID returns the unique identifier for this provider (e.g. "mistral", "minimax").
	ID() string

	// Models returns the list of models available from this provider.
	Models() []Model

	// ChatCompletionStream sends a chat completion request and streams the response.
	ChatCompletionStream(ctx context.Context, apiKey string, req ChatRequest) (<-chan StreamEvent, error)
}

// Model describes an AI model offered by a provider.
type Model struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	ProviderID string             `json:"providerId"`
	Thinking   ThinkingCapability `json:"thinking"`
}

// ThinkingCapability describes whether a model supports reasoning/thinking mode.
type ThinkingCapability struct {
	Supported        bool `json:"supported"`
	EnabledByDefault bool `json:"enabledByDefault"`
	CanDisable       bool `json:"canDisable"`
}

// Message is a single message in a chat conversation.
type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ThinkingState    json.RawMessage `json:"thinking_state,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

// ThinkingConfig controls whether the selected model should use thinking mode.
// A nil value uses the model's default behavior.
type ThinkingConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// ChatRequest is the input for a chat completion.
type ChatRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Thinking    *ThinkingConfig  `json:"thinking,omitempty"`
	WorkspaceID int64            `json:"workspaceId,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
}

// StreamEvent represents a single SSE chunk from a streaming chat completion.
type StreamEvent struct {
	ReasoningContent string           `json:"reasoningContent,omitempty"`
	ThinkingState    json.RawMessage  `json:"thinkingState,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolCalls        []ToolCall       `json:"toolCalls,omitempty"`
	ToolResult       *ToolResult      `json:"toolResult,omitempty"`
	ApprovalRequired *ApprovalRequest `json:"approvalRequired,omitempty"`
	Plan             []PlanStep       `json:"plan,omitempty"`
	Done             bool             `json:"done,omitempty"`
	Error            string           `json:"error,omitempty"`
}

// ApprovalRequest is sent to the frontend when a command needs user approval before execution.
type ApprovalRequest struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// ToolDefinition describes a tool the AI can call.
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall represents the AI's request to call a tool.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the function name and arguments from a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResult is sent back to the frontend after a tool has been executed.
type ToolResult struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Content    string `json:"content"`
}

// PlanStep represents a single step in the AI agent's execution plan.
type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending, in_progress, completed, failed
}

func modelByID(models []Model, modelID string) (Model, bool) {
	for _, model := range models {
		if model.ID == modelID {
			return model, true
		}
	}

	return Model{}, false
}

func thinkingEnabledForRequest(model Model, req ChatRequest) bool {
	if !model.Thinking.Supported {
		return false
	}
	if req.Thinking != nil && req.Thinking.Enabled != nil {
		return *req.Thinking.Enabled
	}

	return model.Thinking.EnabledByDefault
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	return append(json.RawMessage(nil), raw...)
}
