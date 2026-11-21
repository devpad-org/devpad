package settings

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"strings"

	"github.com/boombuler/barcode/qr"
)

// qrQuietZone is the blank margin, in modules, that the QR specification asks
// for around a code so scanners can find it.
const qrQuietZone = 4

// totpQRCode renders a TOTP provisioning URL as a QR code in an SVG data URL.
// It is drawn here rather than by any outside service because the URL carries
// the TOTP secret.
func totpQRCode(provisioningURL string) (string, error) {
	code, err := qr.Encode(provisioningURL, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("encoding QR code: %w", err)
	}

	bounds := code.Bounds()
	size := bounds.Dx() + 2*qrQuietZone

	var path strings.Builder
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !isDark(code.At(x, y)) {
				continue
			}
			// Join each run of dark modules on a row into one rectangle.
			start := x
			for x+1 < bounds.Max.X && isDark(code.At(x+1, y)) {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz",
				start-bounds.Min.X+qrQuietZone, y-bounds.Min.Y+qrQuietZone, x-start+1, x-start+1)
		}
	}

	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
			`<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`,
		size, size, size, size, path.String())
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

func isDark(c color.Color) bool {
	return color.GrayModel.Convert(c).(color.Gray).Y < 128
}
