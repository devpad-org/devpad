package app

import (
	"fmt"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// maxImagesPerRequest bounds the images one model request carries. An agent
// that keeps taking screenshots would otherwise resend every one of them on
// each request; only the latest are sent.
const maxImagesPerRequest = 20

// prepareImages returns the turns as the model should get them: images it
// cannot take, older images past maxImagesPerRequest, and images whose data
// is not loaded become a note in their place. Turns that need no change are
// shared with the input.
func prepareImages(model domain.Model, turns []domain.Turn) []domain.Turn {
	keep := maxImagesPerRequest
	if !model.Vision {
		keep = 0
	}
	// Count back from the end so the newest images are the ones kept.
	seen := 0
	omit := func(image domain.ImagePart) string {
		switch {
		case !model.Vision:
			return fmt.Sprintf("[Image not shown: %s can't view images.]", modelName(model))
		case len(image.Data) == 0:
			return "[Image not shown: it is no longer available.]"
		}
		seen++
		if seen > keep {
			return fmt.Sprintf("[Earlier image not shown: only the latest %d images are sent.]", keep)
		}
		return ""
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
				result := *part.ToolResult
				result.Images = nil
				for k := len(part.ToolResult.Images) - 1; k >= 0; k-- {
					image := part.ToolResult.Images[k]
					if note := omit(image); note != "" {
						result.Content += "\n" + note
						changed = true
						continue
					}
					result.Images = append([]domain.ImagePart{image}, result.Images...)
				}
				if changed {
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
