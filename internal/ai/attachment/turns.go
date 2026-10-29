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
	// dropStep is how many of the oldest images go at once when newer ones
	// need room. Dropping one at a time would change an early turn on every
	// request once a conversation is past the limits, and providers cache
	// only the unchanged start of a request.
	dropStep = 10
	// minKept is the fewest images a step may leave. Where the limits allow
	// so few images that a step would leave fewer, they go one at a time.
	minKept = 5
)

// ForModel returns the turns as the model should get them: images it cannot
// take, older images past the request's limits, and images that are gone
// become a note in their place. Turns that need no change are shared with
// the input.
func ForModel(model domain.Model, turns []domain.Turn) []domain.Turn {
	first := 0
	if model.Vision {
		first = firstSent(availableSizes(turns))
	}
	seen := 0
	omit := func(image domain.ImagePart) string {
		if !model.Vision {
			return fmt.Sprintf("[Image not shown: %s can't view images.]", modelName(model))
		}
		if imageSize(image) == 0 {
			return "[Image not shown: it is no longer available.]"
		}
		index := seen
		seen++
		if index < first {
			return fmt.Sprintf("[Earlier image not shown: only the latest images, at most %d and up to %d MB in all, are sent.]", maxImagesPerRequest, maxImageBytesPerRequest>>20)
		}
		if len(image.Data) == 0 {
			// Its data went between loading the run's images and now.
			return "[Image not shown: it is no longer available.]"
		}
		return ""
	}

	var out []domain.Turn
	for i, turn := range turns {
		var parts []domain.Part
		for j, part := range turn.Parts {
			changed := false
			switch {
			case part.Kind == domain.PartImage && part.Image != nil:
				if note := omit(*part.Image); note != "" {
					// Text parts are joined as they are, so keep the note
					// apart from the words and notes around it.
					if j > 0 && turn.Parts[j-1].Kind == domain.PartText {
						note = "\n" + note
					}
					if j < len(turn.Parts)-1 {
						note += "\n"
					}
					part = domain.Part{Kind: domain.PartText, Text: note}
					changed = true
				}
			case part.Kind == domain.PartToolResult && part.ToolResult != nil && len(part.ToolResult.Images) > 0:
				result := *part.ToolResult
				result.Images = nil
				for _, image := range part.ToolResult.Images {
					if note := omit(image); note != "" {
						result.Content += "\n" + note
						changed = true
					} else {
						result.Images = append(result.Images, image)
					}
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

// Sent returns the images ForModel sends a model that takes images, so a
// run need load the data of only these. Images are dropped only from the
// oldest as turns are added, so every image a later request of the run
// sends is one of these or one added since.
func Sent(turns []domain.Turn) []domain.ImagePart {
	var available []domain.ImagePart
	domain.EachImage(turns, func(image domain.ImagePart) {
		if imageSize(image) > 0 {
			available = append(available, image)
		}
	})
	sizes := make([]int, len(available))
	for i, image := range available {
		sizes[i] = imageSize(image)
	}
	return available[firstSent(sizes):]
}

// firstSent returns the index of the oldest image a request carries, given
// the sizes of the available images, oldest first. Every image from it on
// is sent, and none before it.
func firstSent(sizes []int) int {
	// The newest images that fit. Once one doesn't, no older one is sent.
	first, total := len(sizes), 0
	for first > 0 && len(sizes)-first < maxImagesPerRequest && total+sizes[first-1] <= maxImageBytesPerRequest {
		first--
		total += sizes[first]
	}
	// Round up to a whole step, so the oldest image sent stays the same for
	// several requests, unless that leaves too few. Both only ever grow as
	// images are added, which Sent relies on.
	stepped := (first + dropStep - 1) / dropStep * dropStep
	if len(sizes)-stepped >= minKept {
		return stepped
	}
	return first
}

func availableSizes(turns []domain.Turn) []int {
	var sizes []int
	domain.EachImage(turns, func(image domain.ImagePart) {
		if size := imageSize(image); size > 0 {
			sizes = append(sizes, size)
		}
	})
	return sizes
}

// imageSize is the image's stored size, or for an image made with its data
// and no size, the size of its data. 0 means it is not available.
func imageSize(image domain.ImagePart) int {
	if image.Size > 0 {
		return image.Size
	}
	return len(image.Data)
}

func modelName(model domain.Model) string {
	if model.Name != "" {
		return model.Name
	}
	return model.ID
}
