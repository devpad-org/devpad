package attachment

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// exifJPEG encodes a 32×16 JPEG, red on its left half and blue on its right,
// with an EXIF segment giving the orientation in the given byte order.
func exifJPEG(t *testing.T, orientation uint16, order binary.ByteOrder) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			c := color.RGBA{R: 255, A: 255}
			if x >= 16 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	tiff := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8) // first IFD
	order.PutUint16(tiff[8:], 1) // one entry
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], 3) // SHORT
	order.PutUint32(tiff[14:], 1) // one value
	order.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)

	segment := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	segment = append(segment, payload...)
	data := encoded.Bytes()
	return append(append(append([]byte(nil), data[:2]...), segment...), data[2:]...)
}

func isRed(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > 0xC000 && b < 0x4000
}

func TestJPEGOrientation(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		for orientation := uint16(1); orientation <= 8; orientation++ {
			if got := jpegOrientation(exifJPEG(t, orientation, order)); got != int(orientation) {
				t.Fatalf("%v orientation %d: got %d", order, orientation, got)
			}
		}
	}
	plain := encodedImage(t, "jpeg", 4, 4)
	if got := jpegOrientation(plain); got != 1 {
		t.Fatalf("JPEG without EXIF: got %d, want 1", got)
	}
	if got := jpegOrientation([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}); got != 1 {
		t.Fatalf("truncated segment: got %d, want 1", got)
	}
}

func TestNormalize_TurnsJPEGsUpright(t *testing.T) {
	// Where the red left half of the stored image ends up, as the corner
	// of the upright image it covers.
	tests := []struct {
		orientation         int
		wantWidth, wantHigh int
		redAt               image.Point
		blueAt              image.Point
	}{
		{1, 32, 16, image.Pt(2, 8), image.Pt(29, 8)},
		{2, 32, 16, image.Pt(29, 8), image.Pt(2, 8)},
		{3, 32, 16, image.Pt(29, 8), image.Pt(2, 8)},
		{4, 32, 16, image.Pt(2, 8), image.Pt(29, 8)},
		{5, 16, 32, image.Pt(8, 2), image.Pt(8, 29)},
		{6, 16, 32, image.Pt(8, 2), image.Pt(8, 29)},
		{7, 16, 32, image.Pt(8, 29), image.Pt(8, 2)},
		{8, 16, 32, image.Pt(8, 29), image.Pt(8, 2)},
	}
	for _, tt := range tests {
		got, err := Normalize(context.Background(), exifJPEG(t, uint16(tt.orientation), binary.BigEndian))
		if err != nil {
			t.Fatalf("orientation %d: %v", tt.orientation, err)
		}
		if got.Width != tt.wantWidth || got.Height != tt.wantHigh {
			t.Fatalf("orientation %d: %d×%d, want %d×%d", tt.orientation, got.Width, got.Height, tt.wantWidth, tt.wantHigh)
		}
		decoded, err := jpeg.Decode(bytes.NewReader(got.Data))
		if err != nil {
			t.Fatalf("orientation %d: decode: %v", tt.orientation, err)
		}
		if !isRed(decoded.At(tt.redAt.X, tt.redAt.Y)) || isRed(decoded.At(tt.blueAt.X, tt.blueAt.Y)) {
			t.Fatalf("orientation %d: red half not where expected", tt.orientation)
		}
	}
}
