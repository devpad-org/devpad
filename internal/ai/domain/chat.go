package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	MaxImageBytes    = 5 * 1024 * 1024
	MaxImagesPerTurn = 4
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

// ValidateImageRequest validates image attachments and ensures the model can consume them.
func ValidateImageRequest(model Model, req ChatRequest) error {
	hasImages := false
	for _, turn := range req.Turns {
		imagesInTurn := 0
		for _, part := range turn.Parts {
			if part.Kind != PartImage {
				continue
			}
			hasImages = true
			imagesInTurn++
			if turn.Role != RoleUser {
				return fmt.Errorf("%w: images are only allowed on user turns", ErrInvalidImage)
			}
			if imagesInTurn > MaxImagesPerTurn {
				return fmt.Errorf("%w: too many images in one turn", ErrInvalidImage)
			}
			if part.Image == nil {
				return ErrInvalidImage
			}
			mimeType, ok := normalizeSupportedImageMIME(part.Image.MIMEType)
			if !ok {
				return fmt.Errorf("%w: unsupported mime type", ErrInvalidImage)
			}
			data, err := normalizeImageDataForValidation(part.Image.Data, mimeType)
			if err != nil {
				return err
			}
			if data == "" {
				return fmt.Errorf("%w: empty data", ErrInvalidImage)
			}
			decoded, err := decodeBase64Image(data)
			if err != nil {
				if errors.Is(err, ErrImageTooLarge) {
					return ErrImageTooLarge
				}
				return fmt.Errorf("%w: invalid base64 data", ErrInvalidImage)
			}
			if !imageContentTypeMatches(mimeType, decoded) {
				return fmt.Errorf("%w: mime type does not match content", ErrInvalidImage)
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

func normalizeSupportedImageMIME(mimeType string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(mimeType))
	switch normalized {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return normalized, true
	default:
		return "", false
	}
}

// NormalizeImageData strips an optional data URL prefix and trims surrounding
// whitespace, returning the base64 payload expected by provider adapters.
func NormalizeImageData(data string) string {
	value := strings.TrimSpace(data)
	if payload, _, ok := stripImageDataURL(value); ok {
		return payload
	}
	return value
}

func normalizeImageDataForValidation(data, mimeType string) (string, error) {
	value := strings.TrimSpace(data)
	if !hasDataURLPrefix(value) {
		return value, nil
	}

	payload, dataURLMIME, ok := stripImageDataURL(value)
	if !ok {
		return "", fmt.Errorf("%w: invalid data URL", ErrInvalidImage)
	}
	if dataURLMIME != mimeType {
		return "", fmt.Errorf("%w: data URL mime type mismatch", ErrInvalidImage)
	}
	return payload, nil
}

func stripImageDataURL(value string) (payload, mimeType string, ok bool) {
	if !hasDataURLPrefix(value) {
		return "", "", false
	}
	comma := strings.Index(value, ",")
	if comma < 0 {
		return "", "", false
	}

	metadata := value[len("data:"):comma]
	semicolon := strings.LastIndex(metadata, ";")
	if semicolon < 0 || !strings.EqualFold(metadata[semicolon+1:], "base64") {
		return "", "", false
	}
	mimeType, supported := normalizeSupportedImageMIME(metadata[:semicolon])
	if !supported {
		return "", "", false
	}

	return strings.TrimSpace(value[comma+1:]), mimeType, true
}

func hasDataURLPrefix(value string) bool {
	return len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:")
}

func decodeBase64Image(data string) ([]byte, error) {
	value := strings.TrimSpace(data)
	if len(value) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
		return nil, ErrImageTooLarge
	}
	decodedLen := base64.StdEncoding.DecodedLen(len(value))
	if decodedLen-base64Padding(value) > MaxImageBytes {
		return nil, ErrImageTooLarge
	}
	decoded := make([]byte, decodedLen)
	n, err := base64.StdEncoding.Decode(decoded, []byte(value))
	if err != nil {
		return nil, err
	}
	decoded = decoded[:n]
	if len(decoded) > MaxImageBytes {
		return nil, ErrImageTooLarge
	}
	return decoded, nil
}

func base64Padding(value string) int {
	switch {
	case strings.HasSuffix(value, "=="):
		return 2
	case strings.HasSuffix(value, "="):
		return 1
	default:
		return 0
	}
}

func imageContentTypeMatches(mimeType string, data []byte) bool {
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := http.DetectContentType(sample)
	if detected == mimeType {
		return true
	}
	return mimeType == "image/jpeg" && detected == "image/jpg"
}
