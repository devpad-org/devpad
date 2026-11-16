package domain

import "errors"

var (
	ErrProviderNotFound               = errors.New("AI provider not found")
	ErrModelNotFound                  = errors.New("AI model not found")
	ErrNoAPIKey                       = errors.New("no API key for this provider; add one in Settings")
	ErrAPIKeyUnreadable               = errors.New("the saved API key for this provider can no longer be read; enter it again in Settings")
	ErrThinkingNotSupported           = errors.New("model does not support thinking")
	ErrThinkingCannotBeDisabled       = errors.New("thinking cannot be disabled for this model")
	ErrThinkingEffortNotSupported     = errors.New("thinking effort is not supported for this model")
	ErrThinkingEffortInvalid          = errors.New("invalid thinking effort")
	ErrThinkingEffortRequiresThinking = errors.New("thinking effort requires thinking to be enabled")
	ErrConversationNotFound           = errors.New("conversation not found")
	ErrAgentRunNotFound               = errors.New("agent run not found")
	ErrAgentRunNotActive              = errors.New("agent run is not active")
	ErrAgentNotFound                  = errors.New("AI agent not found")
	ErrAgentNotEditable               = errors.New("AI agent cannot be edited")
	ErrAttachmentNotFound             = errors.New("image attachment not found")
	ErrInvalidImage                   = errors.New("invalid image")
)
