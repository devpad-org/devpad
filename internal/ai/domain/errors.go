package domain

import "errors"

var (
	ErrProviderNotFound               = errors.New("AI provider not found")
	ErrProviderNotEnabled             = errors.New("AI provider is not enabled")
	ErrModelNotFound                  = errors.New("AI model not found")
	ErrNoAPIKey                       = errors.New("no API key configured for provider")
	ErrThinkingNotSupported           = errors.New("model does not support thinking")
	ErrThinkingCannotBeDisabled       = errors.New("thinking cannot be disabled for this model")
	ErrThinkingEffortNotSupported     = errors.New("thinking effort is not supported for this model")
	ErrThinkingEffortInvalid          = errors.New("invalid thinking effort")
	ErrThinkingEffortRequiresThinking = errors.New("thinking effort requires thinking to be enabled")
	ErrImagesNotSupported             = errors.New("model does not support images")
	ErrImageTooLarge                  = errors.New("image exceeds maximum size")
	ErrInvalidImage                   = errors.New("invalid image")
	ErrConversationNotFound           = errors.New("conversation not found")
	ErrAgentRunNotFound               = errors.New("agent run not found")
	ErrAgentRunNotActive              = errors.New("agent run is not active")
	ErrAgentNotFound                  = errors.New("AI agent not found")
	ErrAgentNotEditable               = errors.New("AI agent cannot be edited")
)
