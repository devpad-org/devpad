package domain

import "encoding/json"

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
	ItemID   string           `json:"itemId,omitempty"` // provider item ID (e.g. fc_... for OpenAI Responses API)
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
	Status string `json:"status"`
}
