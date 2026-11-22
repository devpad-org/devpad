package settings

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/boombuler/barcode/qr"
)

func TestTOTPQRCode_DrawsEveryModule(t *testing.T) {
	const url = "otpauth://totp/Devpad:alice?algorithm=SHA1&digits=6&issuer=Devpad&period=30&secret=JBSWY3DPEHPK3PXP"

	dataURL, err := totpQRCode(url)
	if err != nil {
		t.Fatalf("totpQRCode: %v", err)
	}
	encoded, ok := strings.CutPrefix(dataURL, "data:image/svg+xml;base64,")
	if !ok {
		t.Fatalf("not an SVG data URL: %.40q", dataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding data URL: %v", err)
	}
	svg := string(raw)

	want, err := qr.Encode(url, qr.M, qr.Auto)
	if err != nil {
		t.Fatalf("qr.Encode: %v", err)
	}
	dim := want.Bounds().Dx()
	size := dim + 2*qrQuietZone
	if !strings.Contains(svg, fmt.Sprintf(`viewBox="0 0 %d %d"`, size, size)) {
		t.Fatalf("viewBox does not leave the quiet zone: %s", svg[:120])
	}

	dark := make(map[[2]int]bool)
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`).FindAllStringSubmatch(svg, -1) {
		var x, y, w, back int
		fmt.Sscan(m[1], &x)
		fmt.Sscan(m[2], &y)
		fmt.Sscan(m[3], &w)
		fmt.Sscan(m[4], &back)
		if w != back || w < 1 {
			t.Fatalf("malformed run %q", m[0])
		}
		for i := range w {
			dark[[2]int{x + i - qrQuietZone, y - qrQuietZone}] = true
		}
	}

	for y := range dim {
		for x := range dim {
			if got, want := dark[[2]int{x, y}], isDark(want.At(x, y)); got != want {
				t.Fatalf("module (%d,%d): drawn dark = %v, want %v", x, y, got, want)
			}
		}
	}
	if len(dark) == 0 {
		t.Fatal("no modules drawn")
	}
}
