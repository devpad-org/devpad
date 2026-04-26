package domain

import "encoding/json"

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
	Turns       []Turn           `json:"turns"`
	Thinking    *ThinkingConfig  `json:"thinking,omitempty"`
	WorkspaceID int64            `json:"workspaceId,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
}

// ThinkingEnabledForRequest returns whether reasoning mode should be enabled.
func ThinkingEnabledForRequest(model Model, req ChatRequest) bool {
	if !model.Thinking.Supported {
		return false
	}
	if req.Thinking != nil && req.Thinking.Enabled != nil {
		return *req.Thinking.Enabled
	}

	return model.Thinking.EnabledByDefault
}

// ValidateThinkingRequest validates the request against the selected model.
func ValidateThinkingRequest(model Model, req ChatRequest) error {
	if req.Thinking == nil || req.Thinking.Enabled == nil {
		return nil
	}

	enabled := *req.Thinking.Enabled
	if enabled {
		if !model.Thinking.Supported {
			return ErrThinkingNotSupported
		}
		return nil
	}

	if !model.Thinking.Supported {
		return nil
	}
	if !model.Thinking.CanDisable {
		return ErrThinkingCannotBeDisabled
	}

	return nil
}
