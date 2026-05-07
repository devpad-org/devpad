package domain

import (
	"fmt"
	"strings"
)

// ThinkingConfig controls whether the selected model should use thinking mode.
// A nil value uses the model's default behavior.
type ThinkingConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Effort  string `json:"effort,omitempty"`
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
	if req.Thinking != nil && strings.TrimSpace(req.Thinking.Effort) != "" {
		return true
	}

	return model.Thinking.EnabledByDefault
}

// ValidateThinkingRequest validates the request against the selected model.
func ValidateThinkingRequest(model Model, req ChatRequest) error {
	if req.Thinking == nil || req.Thinking.Enabled == nil {
		return validateThinkingEffort(model, req.Thinking)
	}

	enabled := *req.Thinking.Enabled
	if enabled {
		if !model.Thinking.Supported {
			return ErrThinkingNotSupported
		}
		if err := validateThinkingEffort(model, req.Thinking); err != nil {
			return err
		}
		return nil
	}

	if !model.Thinking.Supported {
		return validateThinkingEffort(model, req.Thinking)
	}
	if !model.Thinking.CanDisable {
		return ErrThinkingCannotBeDisabled
	}
	if strings.TrimSpace(req.Thinking.Effort) != "" {
		return ErrThinkingEffortRequiresThinking
	}

	return validateThinkingEffort(model, req.Thinking)
}

func validateThinkingEffort(model Model, thinking *ThinkingConfig) error {
	if thinking == nil {
		return nil
	}

	effort := strings.TrimSpace(thinking.Effort)
	if effort == "" {
		return nil
	}

	if !model.Thinking.Supported || !model.Thinking.SupportsEffortSelection() {
		return ErrThinkingEffortNotSupported
	}
	if !model.Thinking.SupportsEffort(effort) {
		return fmt.Errorf("%w: %s", ErrThinkingEffortInvalid, effort)
	}
	return nil
}
