package media

import (
	"image"
	"image/jpeg"
	"io"

	"github.com/deepteams/webp"
)

// EncodeWebPForTest encodes img as webp so the external media_test package
// can build a webp input for [Process] (the pipeline still accepts webp as
// an input format even though it normalises every stored image to jpeg).
func EncodeWebPForTest(w io.Writer, img image.Image, q float32) error {
	return webp.Encode(w, img, &webp.EncoderOptions{Quality: q})
}

// DecodeJPEGForTest decodes a jpeg stream produced by [Process].
func DecodeJPEGForTest(r io.Reader) (image.Image, error) {
	return jpeg.Decode(r)
}

// DecodeJPEGConfigForTest reads a jpeg header produced by [Process].
func DecodeJPEGConfigForTest(r io.Reader) (image.Config, error) {
	return jpeg.DecodeConfig(r)
}
