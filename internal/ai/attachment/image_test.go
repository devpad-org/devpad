package attachment

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func encodedImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantType   string
		wantWidth  int
		wantHeight int
	}{
		{"small png stays png", encodedImage(t, "png", 40, 30), "image/png", 40, 30},
		{"jpeg stays jpeg", encodedImage(t, "jpeg", 40, 30), "image/jpeg", 40, 30},
		{"gif becomes png", encodedImage(t, "gif", 40, 30), "image/png", 40, 30},
		{"wide image is scaled to the max edge", encodedImage(t, "png", 3136, 1000), "image/png", MaxEdge, 500},
		{"tall image is scaled to the max edge", encodedImage(t, "jpeg", 1000, 3136), "image/jpeg", 500, MaxEdge},
		{"large image is halved before it is scaled", encodedImage(t, "png", 6500, 130), "image/png", MaxEdge, 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(context.Background(), tt.data)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got.MediaType != tt.wantType || got.Width != tt.wantWidth || got.Height != tt.wantHeight {
				t.Fatalf("got %s %d×%d, want %s %d×%d", got.MediaType, got.Width, got.Height, tt.wantType, tt.wantWidth, tt.wantHeight)
			}
			decoded, format, err := image.Decode(bytes.NewReader(got.Data))
			if err != nil {
				t.Fatalf("decode normalized image: %v", err)
			}
			if "image/"+format != got.MediaType || decoded.Bounds().Dx() != got.Width || decoded.Bounds().Dy() != got.Height {
				t.Fatalf("normalized data is %s %v, want %s %d×%d", format, decoded.Bounds(), got.MediaType, got.Width, got.Height)
			}
		})
	}
}

// pngHeader returns a valid PNG whose header claims the given size, bit
// depth and color type, with none of the pixels it claims.
func pngHeader(t *testing.T, width, height uint32, depth, colorType byte) []byte {
	t.Helper()
	data := append([]byte(nil), encodedImage(t, "png", 1, 1)...)
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	data[24], data[25] = depth, colorType
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if config, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != int(width) {
		t.Fatalf("expected a valid PNG header claiming %d pixels wide: %+v, %v", width, config, err)
	}
	return data
}

func TestNormalizeRefusesWhatItCannotSafelyDecode(t *testing.T) {
	// Each is refused from its header alone, before any of it is decoded.
	const rgb, rgba = 2, 6
	huge := pngHeader(t, 100000, 100000, 8, rgb)
	// 25 million pixels is allowed at 8 bits, but at 16 bits a channel
	// decodes to 200 MB.
	deep := pngHeader(t, 5000, 5000, 16, rgba)
	for name, data := range map[string][]byte{"huge": huge, "deep": deep} {
		if _, err := Normalize(context.Background(), data); err == nil || !strings.Contains(err.Error(), "too many pixels") {
			t.Fatalf("%s: err = %v, want it refused for too many pixels", name, err)
		}
	}
	if _, err := Normalize(context.Background(), pngHeader(t, 5000, 5000, 8, rgba)); err == nil || strings.Contains(err.Error(), "too many pixels") {
		t.Fatalf("8-bit 5000×5000: err = %v, want it past the header check", err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{"text", []byte("not an image")},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)},
		{"too many pixels", huge},
		{"too many bytes once decoded", deep},
		{"too large", make([]byte, MaxUploadBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Normalize(context.Background(), tt.data); !errors.Is(err, domain.ErrInvalidImage) {
				t.Fatalf("err = %v, want ErrInvalidImage", err)
			}
		})
	}
}

func TestNormalizeDropsJPEGMetadata(t *testing.T) {
	data := encodedImage(t, "jpeg", 20, 20)
	// Insert an APP1 (EXIF) segment after the SOI marker.
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x10}, []byte("Exif\x00\x00GPS-SECRET")...)
	withExif := append(append(append([]byte(nil), data[:2]...), exif...), data[2:]...)

	got, err := Normalize(context.Background(), withExif)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if bytes.Contains(got.Data, []byte("GPS-SECRET")) {
		t.Fatal("expected the EXIF segment to be removed")
	}
}

func TestProbe(t *testing.T) {
	if width, height, ok := Probe(encodedImage(t, "png", 3, 2)); !ok || width != 3 || height != 2 {
		t.Fatalf("Probe(png) = %d, %d, %v, want 3, 2, true", width, height, ok)
	}
	if _, _, ok := Probe([]byte("package main\n")); ok {
		t.Fatal("expected source code not to be an image")
	}
}

func TestNormalizeWaitsForAFreeSlot(t *testing.T) {
	for range maxConcurrent {
		slots <- struct{}{}
	}
	t.Cleanup(func() {
		for range maxConcurrent {
			<-slots
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Normalize(ctx, encodedImage(t, "png", 2, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled while every slot is taken", err)
	}
}
