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
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"providerId"`
}

// Message is a single message in a chat conversation.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ChatRequest is the input for a chat completion.
type ChatRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	WorkspaceID int64            `json:"workspaceId,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
}

// StreamEvent represents a single SSE chunk from a streaming chat completion.
type StreamEvent struct {
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"toolCalls,omitempty"`
	ToolResult *ToolResult `json:"toolResult,omitempty"`
	Done       bool        `json:"done,omitempty"`
	Error      string      `json:"error,omitempty"`
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
