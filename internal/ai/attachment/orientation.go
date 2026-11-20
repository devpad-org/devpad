package attachment

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// jpegOrientation returns the EXIF orientation of a JPEG, 1 to 8, or 1 when it
// has none. Cameras store a photo as the sensor read it and record here how
// to turn it upright; re-encoding drops the record, so the turn is applied.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xFF {
			i++ // fill byte
			continue
		}
		// The image data starts at SOS; EXIF comes before it.
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		if length < 2 || i+2+length > len(data) {
			return 1
		}
		segment := data[i+4 : i+2+length]
		if marker == 0xE1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return exifOrientation(segment[6:])
		}
		i += 2 + length
	}
	return 1
}

// exifOrientation reads the orientation tag from a TIFF header and its first
// IFD.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	entries := int(order.Uint16(tiff[ifd:]))
	for k := range entries {
		entry := ifd + 2 + 12*k
		if entry+12 > len(tiff) {
			return 1
		}
		const orientationTag, shortType = 0x0112, 3
		if order.Uint16(tiff[entry:]) == orientationTag && order.Uint16(tiff[entry+2:]) == shortType {
			if value := int(order.Uint16(tiff[entry+8:])); value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}

// orient turns the image upright for its EXIF orientation.
func orient(src image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return src
	}
	bounds := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, bounds.Min, draw.Src)
	}
	w, h := bounds.Dx(), bounds.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := range dh {
		for dx := range dw {
			var sx, sy int
			switch orientation {
			case 2: // mirrored
				sx, sy = w-1-dx, dy
			case 3: // upside down
				sx, sy = w-1-dx, h-1-dy
			case 4: // mirrored upside down
				sx, sy = dx, h-1-dy
			case 5: // mirrored, turned a quarter
				sx, sy = dy, dx
			case 6: // needs a quarter turn clockwise
				sx, sy = dy, h-1-dx
			case 7: // mirrored, turned a quarter the other way
				sx, sy = w-1-dy, h-1-dx
			case 8: // needs a quarter turn counterclockwise
				sx, sy = w-1-dy, dx
			}
			s := rgba.PixOffset(rgba.Rect.Min.X+sx, rgba.Rect.Min.Y+sy)
			d := dst.PixOffset(dx, dy)
			copy(dst.Pix[d:d+4], rgba.Pix[s:s+4])
		}
	}
	return dst
}
