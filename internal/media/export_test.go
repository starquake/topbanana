package media

import (
	"image"
	"image/jpeg"
	"io"
)

// DecodeJPEGForTest decodes a jpeg stream produced by [Process].
func DecodeJPEGForTest(r io.Reader) (image.Image, error) {
	return jpeg.Decode(r)
}

// DecodeJPEGConfigForTest reads a jpeg header produced by [Process].
func DecodeJPEGConfigForTest(r io.Reader) (image.Config, error) {
	return jpeg.DecodeConfig(r)
}
