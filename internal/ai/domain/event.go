package domain

import "encoding/json"

// ProviderEvent represents a single provider-stream chunk before orchestration.
type ProviderEvent struct {
	TextDelta      string
	ReasoningDelta string
	ReasoningState json.RawMessage
	ToolCalls      []ToolCall
	Done           bool
	Err            error
}

// ClientEvent is the normalized event emitted by the orchestrator.
type ClientEvent struct {
	TextDelta      string
	ReasoningDelta string
	ReasoningState json.RawMessage
	ToolCalls      []ToolCall
	ToolResult     *ToolResultPart
	Approval       *ApprovalRequest
	ApprovalResult *ApprovalResult
	Plan           []PlanStep
	Done           bool
	ErrorMessage   string
}

// StreamEvent represents a single SSE chunk from a streaming chat completion.
type StreamEvent struct {
	ReasoningContent string           `json:"reasoningContent,omitempty"`
	ThinkingState    json.RawMessage  `json:"thinkingState,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolCalls        []ToolCall       `json:"toolCalls,omitempty"`
	ToolResult       *ToolResult      `json:"toolResult,omitempty"`
	ApprovalRequired *ApprovalRequest `json:"approvalRequired,omitempty"`
	ApprovalResolved *ApprovalResult  `json:"approvalResolved,omitempty"`
	Plan             []PlanStep       `json:"plan,omitempty"`
	Done             bool             `json:"done,omitempty"`
	Error            string           `json:"error,omitempty"`
}

// ApprovalRequest is sent to the frontend when a command needs user approval before execution.
type ApprovalRequest struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// ApprovalResult is sent after a pending approval has been resolved.
type ApprovalResult struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

// CloneRawMessage returns an owned copy of the raw JSON payload.
func CloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	return append(json.RawMessage(nil), raw...)
}
