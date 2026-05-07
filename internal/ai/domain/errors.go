package domain

import "errors"

var (
	ErrProviderNotFound         = errors.New("AI provider not found")
	ErrProviderNotEnabled       = errors.New("AI provider is not enabled")
	ErrModelNotFound            = errors.New("AI model not found")
	ErrNoAPIKey                 = errors.New("no API key configured for provider")
	ErrThinkingNotSupported     = errors.New("model does not support thinking")
	ErrThinkingCannotBeDisabled = errors.New("thinking cannot be disabled for this model")
	ErrConversationNotFound     = errors.New("conversation not found")
	ErrAgentRunNotFound         = errors.New("agent run not found")
	ErrAgentRunNotActive        = errors.New("agent run is not active")
	ErrAgentNotFound            = errors.New("AI agent not found")
	ErrAgentNotEditable         = errors.New("AI agent cannot be edited")
)
