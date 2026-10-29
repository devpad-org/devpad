package shared

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ImageURLStyle is how an OpenAI-style chat API takes an image_url part.
type ImageURLStyle int

const (
	// ImageURLObject is OpenAI's {"image_url": {"url": "data:..."}}.
	ImageURLObject ImageURLStyle = iota
	// ImageURLString is Mistral's {"image_url": "data:..."}.
	ImageURLString
)

// DataURL returns the image as a base64 data URL.
func DataURL(image domain.ImagePart) string {
	return "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

// ChatContent returns a chat-completions message content: the text alone
// when there are no images, so text-only requests are unchanged, and
// otherwise the images followed by the text as content parts.
func ChatContent(text string, images []domain.ImagePart, style ImageURLStyle) any {
	if len(images) == 0 {
		return text
	}
	parts := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		parts = append(parts, imageURLPart(image, style))
	}
	if text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	return parts
}

// ToolImagesContent returns the content of a user message carrying the images
// the tool results returned, which chat-completions APIs only take from the
// user. It reports false when the results returned no images.
func ToolImagesContent(results []domain.ToolResultPart, style ImageURLStyle) (any, bool) {
	var images []domain.ImagePart
	var names []string
	for _, result := range results {
		if len(result.Images) == 0 {
			continue
		}
		images = append(images, result.Images...)
		names = append(names, result.Name)
	}
	if len(images) == 0 {
		return nil, false
	}
	text := fmt.Sprintf("The images returned by %s:", strings.Join(names, ", "))
	parts := []map[string]any{{"type": "text", "text": text}}
	for _, image := range images {
		parts = append(parts, imageURLPart(image, style))
	}
	return parts, true
}

func imageURLPart(image domain.ImagePart, style ImageURLStyle) map[string]any {
	url := DataURL(image)
	if style == ImageURLString {
		return map[string]any{"type": "image_url", "image_url": url}
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}
