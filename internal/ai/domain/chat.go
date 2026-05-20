package domain

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const MaxImageBytes = 5 * 1024 * 1024

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

// ValidateImageRequest validates image attachments and ensures the model can consume them.
func ValidateImageRequest(model Model, req ChatRequest) error {
	hasImages := false
	for _, turn := range req.Turns {
		for _, part := range turn.Parts {
			if part.Kind != PartImage {
				continue
			}
			hasImages = true
			if part.Image == nil {
				return ErrInvalidImage
			}
			if !isSupportedImageMIME(part.Image.MIMEType) {
				return fmt.Errorf("%w: unsupported mime type", ErrInvalidImage)
			}
			data := NormalizeImageData(part.Image.Data)
			if data == "" {
				return fmt.Errorf("%w: empty data", ErrInvalidImage)
			}
			size, err := decodedBase64Size(data)
			if err != nil {
				return fmt.Errorf("%w: invalid base64 data", ErrInvalidImage)
			}
			if size > MaxImageBytes {
				return ErrImageTooLarge
			}
		}
	}
	if hasImages && !model.Vision {
		return ErrImagesNotSupported
	}
	return nil
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

func isSupportedImageMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

// NormalizeImageData strips an optional data URL prefix and trims surrounding
// whitespace, returning the base64 payload expected by provider adapters.
func NormalizeImageData(data string) string {
	value := strings.TrimSpace(data)
	if comma := strings.Index(value, ","); strings.HasPrefix(value, "data:") && comma >= 0 {
		return value[comma+1:]
	}
	return value
}

func decodedBase64Size(data string) (int, error) {
	value := NormalizeImageData(data)
	decodedLen := base64.StdEncoding.DecodedLen(len(value))
	if decodedLen > MaxImageBytes+2 {
		return MaxImageBytes + 1, nil
	}
	decoded := make([]byte, decodedLen)
	n, err := base64.StdEncoding.Decode(decoded, []byte(value))
	if err != nil {
		return 0, err
	}
	return n, nil
}
