package domain

import "encoding/json"

// Role is the normalized actor for a chat turn.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// PartKind identifies the kind of data stored in a turn part.
type PartKind string

const (
	PartText       PartKind = "text"
	PartThinking   PartKind = "thinking"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
	PartImage      PartKind = "image"
)

// Turn is the provider-agnostic internal representation of a chat exchange.
type Turn struct {
	Role  Role
	Parts []Part
}

// Part stores one normalized piece of a turn.
type Part struct {
	Kind       PartKind
	Text       string          // PartText
	Thinking   *ThinkingPart   // PartThinking
	ToolCall   *ToolCall       // PartToolCall
	ToolResult *ToolResultPart // PartToolResult
	Image      *ImagePart      // PartImage
}

// ThinkingPart holds reasoning output with an opaque provider state for round-trip.
// State carries the provider-specific blob (e.g. Anthropic signature, OpenAI encrypted_content)
// required to continue a multi-turn reasoning conversation.
type ThinkingPart struct {
	Text  string
	State json.RawMessage
}

// ToolResultPart is the normalized form of a tool execution result.
// Tool results are always carried inside a user turn.
type ToolResultPart struct {
	ToolCallID string
	Name       string
	Content    string
	IsError    bool
	// Images are pictures the tool returned alongside Content, such as an
	// image file read_file opened.
	Images []ImagePart
}

// ImagePart refers to a stored image attachment. Turns carry only the
// reference: Data is filled from the attachment store just before a run
// sends the turn to a provider, and is never serialized, so transcripts
// saved as JSON stay small.
type ImagePart struct {
	AttachmentID int64
	MediaType    string
	Width        int
	Height       int
	Data         []byte `json:"-"`
}

// NewImagePart creates an image part for the given attachment reference.
func NewImagePart(image ImagePart) Part {
	return Part{Kind: PartImage, Image: &image}
}

// CloneImages returns an owned copy of the image references, sharing Data,
// which is never modified once loaded.
func CloneImages(images []ImagePart) []ImagePart {
	if len(images) == 0 {
		return nil
	}
	return append([]ImagePart(nil), images...)
}

// NewTextTurn creates a turn with a single text part when text is present.
func NewTextTurn(role Role, text string) Turn {
	turn := Turn{Role: role}
	if text != "" {
		turn.Parts = append(turn.Parts, Part{Kind: PartText, Text: text})
	}
	return turn
}

// NewToolResultTurn creates a user turn with a single tool-result part.
func NewToolResultTurn(toolCallID, name, content string, isError bool) Turn {
	return NewToolResultTurnFromPart(ToolResultPart{
		ToolCallID: toolCallID,
		Name:       name,
		Content:    content,
		IsError:    isError,
	})
}

// NewToolResultTurnFromPart creates a user turn holding the given tool result,
// images included.
func NewToolResultTurnFromPart(result ToolResultPart) Turn {
	result.Images = CloneImages(result.Images)
	return Turn{
		Role:  RoleUser,
		Parts: []Part{{Kind: PartToolResult, ToolResult: &result}},
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

// Images returns the image parts attached to the turn.
func (t Turn) Images() []ImagePart {
	var images []ImagePart
	for _, part := range t.Parts {
		if part.Kind == PartImage && part.Image != nil {
			images = append(images, *part.Image)
		}
	}
	return images
}

// ThinkingText returns the aggregated thinking content for the turn.
func (t Turn) ThinkingText() string {
	text := ""
	for _, part := range t.Parts {
		if part.Kind == PartThinking && part.Thinking != nil {
			text += part.Thinking.Text
		}
	}
	return text
}

// ThinkingState returns the last non-empty thinking state attached to the turn.
func (t Turn) ThinkingState() json.RawMessage {
	for i := len(t.Parts) - 1; i >= 0; i-- {
		part := t.Parts[i]
		if part.Kind == PartThinking && part.Thinking != nil && len(part.Thinking.State) > 0 {
			return CloneRawMessage(part.Thinking.State)
		}
	}
	return nil
}

// ToolCalls returns the normalized tool calls attached to the turn.
func (t Turn) ToolCalls() []ToolCall {
	toolCalls := make([]ToolCall, 0)
	for _, part := range t.Parts {
		if part.Kind == PartToolCall && part.ToolCall != nil {
			toolCalls = append(toolCalls, cloneToolCall(*part.ToolCall))
		}
	}
	return toolCalls
}

// ToolResults returns all tool result parts attached to the turn.
func (t Turn) ToolResults() []ToolResultPart {
	results := make([]ToolResultPart, 0)
	for _, part := range t.Parts {
		if part.Kind == PartToolResult && part.ToolResult != nil {
			results = append(results, *part.ToolResult)
		}
	}
	return results
}

// ToolResult returns the first tool result part attached to the turn.
func (t Turn) ToolResult() *ToolResultPart {
	for _, part := range t.Parts {
		if part.Kind == PartToolResult && part.ToolResult != nil {
			result := *part.ToolResult
			return &result
		}
	}
	return nil
}

func cloneToolCall(toolCall ToolCall) ToolCall {
	return ToolCall{
		ID:     toolCall.ID,
		ItemID: toolCall.ItemID,
		Type:   toolCall.Type,
		Function: ToolCallFunction{
			Name:      toolCall.Function.Name,
			Arguments: toolCall.Function.Arguments,
		},
	}
}
