package attachment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.data)
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

func TestNormalizeRefusesWhatItCannotSafelyDecode(t *testing.T) {
	// A PNG header claiming 100000×100000 pixels: refused from its header
	// alone, before any of it is decoded.
	huge := encodedImage(t, "png", 1, 1)
	huge = append([]byte(nil), huge...)
	copy(huge[16:20], []byte{0x00, 0x01, 0x86, 0xA0})
	copy(huge[20:24], []byte{0x00, 0x01, 0x86, 0xA0})
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	if config, err := png.DecodeConfig(bytes.NewReader(huge)); err != nil || config.Width != 100000 {
		t.Fatalf("expected a valid PNG header claiming 100000 pixels wide: %+v, %v", config, err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{"text", []byte("not an image")},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)},
		{"too many pixels", huge},
		{"too large", make([]byte, MaxUploadBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Normalize(tt.data); !errors.Is(err, domain.ErrInvalidImage) {
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

	got, err := Normalize(withExif)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if bytes.Contains(got.Data, []byte("GPS-SECRET")) {
		t.Fatal("expected the EXIF segment to be removed")
	}
}

func TestIsImage(t *testing.T) {
	if !IsImage(encodedImage(t, "png", 2, 2)) {
		t.Fatal("expected a PNG to be an image")
	}
	if IsImage([]byte("package main\n")) {
		t.Fatal("expected source code not to be an image")
	}
}
