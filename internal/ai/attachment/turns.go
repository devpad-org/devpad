package attachment

import (
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	// maxImagesPerRequest and maxImageBytesPerRequest bound the images one
	// model request carries. An agent that keeps taking screenshots would
	// otherwise resend every one of them on each request, and a request
	// past a provider's size limit (32 MB at Anthropic, base64 included)
	// fails each time the conversation goes on. Only the latest are sent.
	maxImagesPerRequest     = 20
	maxImageBytesPerRequest = 16 << 20
)

// ForModel returns the turns as the model should get them: images it cannot
// take, older images past the request's limits, and images whose data is not
// loaded become a note in their place. Turns that need no change are shared
// with the input.
func ForModel(model domain.Model, turns []domain.Turn) []domain.Turn {
	// Count back from the end so the newest images are the ones kept. Once
	// one doesn't fit, no older one is sent either.
	seen, size, full := 0, 0, false
	omit := func(image domain.ImagePart) string {
		switch {
		case !model.Vision:
			return fmt.Sprintf("[Image not shown: %s can't view images.]", modelName(model))
		case len(image.Data) == 0:
			return "[Image not shown: it is no longer available.]"
		}
		if !full && seen < maxImagesPerRequest && size+len(image.Data) <= maxImageBytesPerRequest {
			seen++
			size += len(image.Data)
			return ""
		}
		full = true
		return fmt.Sprintf("[Earlier image not shown: only the latest %d images, up to %d MB in all, are sent.]", maxImagesPerRequest, maxImageBytesPerRequest>>20)
	}

	var out []domain.Turn
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		var parts []domain.Part
		for j := len(turn.Parts) - 1; j >= 0; j-- {
			part := turn.Parts[j]
			changed := false
			switch {
			case part.Kind == domain.PartImage && part.Image != nil:
				if note := omit(*part.Image); note != "" {
					part = domain.Part{Kind: domain.PartText, Text: note}
					changed = true
				}
			case part.Kind == domain.PartToolResult && part.ToolResult != nil && len(part.ToolResult.Images) > 0:
				// Walk the images newest first, then put the kept ones and
				// the notes back in their order.
				images := part.ToolResult.Images
				kept := make([]domain.ImagePart, len(images))
				notes := make([]string, len(images))
				for k := len(images) - 1; k >= 0; k-- {
					if notes[k] = omit(images[k]); notes[k] != "" {
						changed = true
					} else {
						kept[k] = images[k]
					}
				}
				if changed {
					result := *part.ToolResult
					result.Images = nil
					for k := range images {
						if notes[k] != "" {
							result.Content += "\n" + notes[k]
						} else {
							result.Images = append(result.Images, kept[k])
						}
					}
					part.ToolResult = &result
				}
			}
			if changed && parts == nil {
				parts = append([]domain.Part(nil), turn.Parts...)
			}
			if parts != nil {
				parts[j] = part
			}
		}
		if parts != nil {
			if out == nil {
				out = append([]domain.Turn(nil), turns...)
			}
			out[i] = domain.Turn{Role: turn.Role, Parts: parts}
		}
	}
	if out == nil {
		return turns
	}
	return out
}

func modelName(model domain.Model) string {
	if model.Name != "" {
		return model.Name
	}
	return model.ID
}
