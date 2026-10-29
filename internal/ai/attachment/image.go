// Package attachment prepares images people and tools hand to AI models.
package attachment

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	// MaxUploadBytes caps the encoded image a person or tool may hand over.
	MaxUploadBytes = 10 << 20
	// MaxEdge is the longest side an image is sent at. Anthropic downscales
	// anything larger before the model sees it, and the other providers
	// read text in a screenshot well at this size.
	MaxEdge = 1568
	// maxSourcePixels refuses images whose decoded size alone would use
	// hundreds of megabytes, before any of it is decoded.
	maxSourcePixels = 40_000_000
	// maxPNGBytes is where a PNG is swapped for a JPEG: base64 of anything
	// larger passes Anthropic's 5 MB per-image limit.
	maxPNGBytes = 3_750_000
	jpegQuality = 85
)

// Image is a normalized image: PNG or JPEG, no larger than MaxEdge on its
// longest side, with any metadata the original carried removed.
type Image struct {
	MediaType string
	Width     int
	Height    int
	Data      []byte
}

// Normalize decodes a PNG, JPEG, GIF (its first frame) or WebP image, scales
// it down to fit MaxEdge, and re-encodes it. Anything else, or an image too
// large to decode safely, is refused with domain.ErrInvalidImage.
func Normalize(data []byte) (Image, error) {
	if len(data) > MaxUploadBytes {
		return Image{}, fmt.Errorf("%w: larger than %d MB", domain.ErrInvalidImage, MaxUploadBytes>>20)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("%w: not a PNG, JPEG, GIF or WebP image", domain.ErrInvalidImage)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Image{}, fmt.Errorf("%w: empty image", domain.ErrInvalidImage)
	}
	if int64(config.Width)*int64(config.Height) > maxSourcePixels {
		return Image{}, fmt.Errorf("%w: %d×%d is too many pixels", domain.ErrInvalidImage, config.Width, config.Height)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("%w: decoding %s: %v", domain.ErrInvalidImage, format, err)
	}

	scaled := scaleToFit(decoded, MaxEdge)
	bounds := scaled.Bounds()
	out := Image{Width: bounds.Dx(), Height: bounds.Dy()}

	// Photos stay JPEG. Screenshots and diagrams keep PNG's sharp text
	// unless the PNG would be too large to send.
	if format != "jpeg" {
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaled); err != nil {
			return Image{}, fmt.Errorf("encoding png: %w", err)
		}
		if buf.Len() <= maxPNGBytes {
			out.MediaType = "image/png"
			out.Data = buf.Bytes()
			return out, nil
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flatten(scaled), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return Image{}, fmt.Errorf("encoding jpeg: %w", err)
	}
	out.MediaType = "image/jpeg"
	out.Data = buf.Bytes()
	return out, nil
}

// IsImage reports whether data starts like an image Normalize accepts.
func IsImage(data []byte) bool {
	_, _, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil
}

func scaleToFit(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxEdge && height <= maxEdge {
		return src
	}
	if width >= height {
		height = max(1, height*maxEdge/width)
		width = maxEdge
	} else {
		width = max(1, width*maxEdge/height)
		height = maxEdge
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Src, nil)
	return dst
}

// flatten draws the image over white, since JPEG has no transparency and
// would otherwise turn transparent pixels black.
func flatten(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Over)
	return dst
}
