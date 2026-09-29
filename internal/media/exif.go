package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
)

// EXIF Orientation values (TIFF tag 0x0112): how the stored pixels must be
// transformed to display upright.
const (
	orientationNormal     = 1
	orientationFlipH      = 2
	orientationRotate180  = 3
	orientationFlipV      = 4
	orientationTranspose  = 5
	orientationRotate90   = 6
	orientationTransverse = 7
	orientationRotate270  = 8
)

const (
	markerAPP1     = 0xE1
	tagOrientation = 0x0112
	typeShort      = 3
	ifdEntrySize   = 12
	tiffHeaderSize = 8
	ifdCountSize   = 2
	tiffMagic      = 42
)

const exifHeader = "Exif\x00\x00"

// jpegOrientation returns the EXIF Orientation of a jpeg, or orientationNormal
// when there is no EXIF segment or it is malformed or out of range.
func jpegOrientation(raw []byte) int {
	orientation := orientationNormal
	jpegSegments(raw, func(marker byte, payload []byte) bool {
		if marker != markerAPP1 || !bytes.HasPrefix(payload, []byte(exifHeader)) {
			return true
		}
		orientation = tiffOrientation(payload[len(exifHeader):])

		return false
	})

	return orientation
}

// tiffOrientation reads the Orientation tag from IFD0 of a TIFF block.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < tiffHeaderSize {
		return orientationNormal
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return orientationNormal
	}
	if order.Uint16(tiff[2:]) != tiffMagic {
		return orientationNormal
	}
	ifd := int64(order.Uint32(tiff[4:]))
	if ifd+ifdCountSize > int64(len(tiff)) {
		return orientationNormal
	}
	count := int64(order.Uint16(tiff[ifd:]))
	for n := range count {
		entry := ifd + ifdCountSize + n*ifdEntrySize
		if entry+ifdEntrySize > int64(len(tiff)) {
			return orientationNormal
		}
		if order.Uint16(tiff[entry:]) != tagOrientation {
			continue
		}
		if order.Uint16(tiff[entry+2:]) != typeShort || order.Uint32(tiff[entry+4:]) != 1 {
			return orientationNormal
		}
		v := int(order.Uint16(tiff[entry+8:]))
		if v < orientationNormal || v > orientationRotate270 {
			return orientationNormal
		}

		return v
	}

	return orientationNormal
}

// applyOrientation returns img transformed upright for the EXIF orientation o.
func applyOrientation(img image.Image, o int) image.Image {
	if o <= orientationNormal || o > orientationRotate270 {
		return img
	}
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= orientationTranspose {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			sx, sy := sourcePoint(o, x, y, w, h)
			si := sy*src.Stride + sx*rgbaPixelBytes
			di := y*dst.Stride + x*rgbaPixelBytes
			copy(dst.Pix[di:di+rgbaPixelBytes], src.Pix[si:si+rgbaPixelBytes])
		}
	}

	return dst
}

// sourcePoint maps destination pixel (x, y) to its source pixel in a w x h
// image for orientation o.
func sourcePoint(o, x, y, w, h int) (sx, sy int) {
	switch o {
	case orientationFlipH:
		return w - 1 - x, y
	case orientationRotate180:
		return w - 1 - x, h - 1 - y
	case orientationFlipV:
		return x, h - 1 - y
	case orientationTranspose:
		return y, x
	case orientationRotate90:
		return y, h - 1 - x
	case orientationTransverse:
		return w - 1 - y, h - 1 - x
	case orientationRotate270:
		return w - 1 - y, x
	default:
		return x, y
	}
}

// toRGBA returns img as an [image.RGBA] whose bounds start at the origin.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return rgba
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Rect, img, b.Min, draw.Src)

	return rgba
}
