package domain

import "encoding/json"

// ProviderEvent represents a single provider-stream chunk before orchestration.
type ProviderEvent struct {
	TextDelta      string
	ReasoningDelta string
	ReasoningState json.RawMessage
	ToolCalls      []ToolCall
	Usage          *Usage // sent once, before the stream ends, when the provider reports it
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
	Question       *UserQuestionRequest
	QuestionResult *UserQuestionResult
	Plan           []PlanStep
	ContextSize    *ContextSize
	Usage          *Usage // what one provider request was billed for
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
	ContextSize      *ContextSize     `json:"contextSize,omitempty"`
	Done             bool             `json:"done,omitempty"`
	Error            string           `json:"error,omitempty"`
}

// ApprovalRequest is sent to the frontend when a tool call needs user
// approval before execution. Title names what is being approved and Command
// describes it; an empty Title means Command is a sudo shell command.
type ApprovalRequest struct {
	ID      string           `json:"id"`
	Title   string           `json:"title,omitempty"`
	Command string           `json:"command"`
	Details []ApprovalDetail `json:"details,omitempty"`
}

// ApprovalDetail is one fact about an action awaiting approval. Text marks
// a value the model wrote, such as a task's instructions, which the client
// shows verbatim and set apart from the server's own wording.
type ApprovalDetail struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Text  bool   `json:"text,omitempty"`
}

// ApprovalResult is sent after a pending approval has been resolved.
type ApprovalResult struct {
	ID      string           `json:"id"`
	Title   string           `json:"title,omitempty"`
	Command string           `json:"command"`
	Details []ApprovalDetail `json:"details,omitempty"`
	Status  string           `json:"status"`
}

// CloneRawMessage returns an owned copy of the raw JSON payload.
func CloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	return append(json.RawMessage(nil), raw...)
}
