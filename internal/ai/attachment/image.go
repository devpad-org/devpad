// Package attachment prepares images people and tools hand to AI models.
package attachment

import (
	"bytes"
	"context"
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
	// maxSourcePixels and maxDecodedBytes refuse, from the header alone,
	// images that would take seconds or hundreds of megabytes to decode and
	// scale. A file of a few hundred kilobytes can claim to be either.
	maxSourcePixels = 40_000_000
	maxDecodedBytes = 128 << 20
	// maxConcurrent bounds the images normalized at once across devpad. Each
	// can briefly use a few hundred megabytes, and tools in a workspace,
	// which is not trusted, can ask for as many as they like.
	maxConcurrent = 2
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

// slots holds one token for each image being normalized.
var slots = make(chan struct{}, maxConcurrent)

// Normalize decodes a PNG, JPEG, GIF (its first frame) or WebP image, scales
// it down to fit MaxEdge, and re-encodes it. Anything else, or an image too
// large to decode safely, is refused with domain.ErrInvalidImage. It waits
// while maxConcurrent other images are being normalized.
func Normalize(ctx context.Context, data []byte) (Image, error) {
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
	pixels := int64(config.Width) * int64(config.Height)
	if pixels > maxSourcePixels || pixels*bytesPerPixel(config.ColorModel) > maxDecodedBytes {
		return Image{}, fmt.Errorf("%w: %d×%d is too many pixels", domain.ErrInvalidImage, config.Width, config.Height)
	}

	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}
	defer func() { <-slots }()

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

// Probe reports the size of an image Normalize may accept, from its header,
// and whether data starts like one at all.
func Probe(data []byte) (width, height int, ok bool) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// bytesPerPixel is roughly what the decoder keeps for each pixel of an image
// with the given color model.
func bytesPerPixel(model color.Model) int64 {
	if _, ok := model.(color.Palette); ok {
		return 1
	}
	switch model {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	default:
		return 4
	}
}

func scaleToFit(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxEdge && height <= maxEdge {
		return src
	}
	dstWidth, dstHeight := maxEdge, maxEdge
	if width >= height {
		dstHeight = max(1, height*maxEdge/width)
	} else {
		dstWidth = max(1, width*maxEdge/height)
	}
	// A kernel scale holds four float64s for each destination column of
	// every source row: hundreds of megabytes for a large source. Halving
	// first, where ApproxBiLinear averages each 2×2 block, leaves it at
	// most twice the destination's height to scale.
	for max(width, height) > 2*maxEdge {
		width, height = max(1, width/2), max(1, height/2)
		half := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.ApproxBiLinear.Scale(half, half.Bounds(), src, src.Bounds(), xdraw.Src, nil)
		src = half
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
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
