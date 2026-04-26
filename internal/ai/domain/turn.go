package domain

import "encoding/json"

// Role is the normalized actor for a chat turn.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartKind identifies the kind of data stored in a turn part.
type PartKind string

const (
	PartText       PartKind = "text"
	PartReasoning  PartKind = "reasoning"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
)

// Turn is the provider-agnostic internal representation of a chat exchange.
type Turn struct {
	Role  Role
	Parts []Part
}

// Part stores one normalized piece of a turn.
type Part struct {
	Kind          PartKind
	Text          string
	ToolCall      *ToolCall
	ToolResult    *ToolResultPart
	ProviderState json.RawMessage
}

// ToolResultPart is the normalized form of a tool execution result.
type ToolResultPart struct {
	ToolCallID string
	Name       string
	Content    string
	IsError    bool
}

// NewTextTurn creates a turn with a single text part when text is present.
func NewTextTurn(role Role, text string) Turn {
	turn := Turn{Role: role}
	if text != "" {
		turn.Parts = append(turn.Parts, Part{Kind: PartText, Text: text})
	}

	return turn
}

// NewToolResultTurn creates a tool turn with a single tool-result part.
func NewToolResultTurn(toolCallID, name, content string, isError bool) Turn {
	return Turn{
		Role: RoleTool,
		Parts: []Part{{
			Kind: PartToolResult,
			ToolResult: &ToolResultPart{
				ToolCallID: toolCallID,
				Name:       name,
				Content:    content,
				IsError:    isError,
			},
		}},
	}
}

// Text returns the aggregated text content for the turn.
func (t Turn) Text() string {
	text := ""
	for _, part := range t.Parts {
		if part.Kind == PartText {
			text += part.Text
		}
	}

	return text
}

// ReasoningText returns the aggregated reasoning content for the turn.
func (t Turn) ReasoningText() string {
	text := ""
	for _, part := range t.Parts {
		if part.Kind == PartReasoning {
			text += part.Text
		}
	}

	return text
}

// ReasoningState returns the last non-empty reasoning state attached to the turn.
func (t Turn) ReasoningState() json.RawMessage {
	for i := len(t.Parts) - 1; i >= 0; i-- {
		part := t.Parts[i]
		if part.Kind == PartReasoning && len(part.ProviderState) > 0 {
			return CloneRawMessage(part.ProviderState)
		}
	}

	return nil
}

// ToolCalls returns the normalized tool calls attached to the turn.
func (t Turn) ToolCalls() []ToolCall {
	toolCalls := make([]ToolCall, 0, len(t.Parts))
	for _, part := range t.Parts {
		if part.Kind == PartToolCall && part.ToolCall != nil {
			toolCalls = append(toolCalls, cloneToolCall(*part.ToolCall))
		}
	}

	return toolCalls
}

// ToolResult returns the first normalized tool result attached to the turn.
func (t Turn) ToolResult() *ToolResultPart {
	for _, part := range t.Parts {
		if part.Kind == PartToolResult && part.ToolResult != nil {
			result := *part.ToolResult
			return &result
		}
	}

	return nil
}

// ToolCallID returns the tool call ID for a tool-result turn when present.
func (t Turn) ToolCallID() string {
	result := t.ToolResult()
	if result == nil {
		return ""
	}

	return result.ToolCallID
}

func cloneToolCall(toolCall ToolCall) ToolCall {
	return ToolCall{
		ID:   toolCall.ID,
		Type: toolCall.Type,
		Function: ToolCallFunction{
			Name:      toolCall.Function.Name,
			Arguments: toolCall.Function.Arguments,
		},
	}
}
