package media

import (
	"image"
	"io"

	"github.com/deepteams/webp"
)

// The *ForTest wrappers expose the webp codec to the external media_test
// package. The codec is concurrency-safe and deterministic since the
// internal/dsp gamma-table sync.Once and the lossy encoderPool mbInfo
// reset landed upstream.

// EncodeWebPForTest encodes img at quality q.
func EncodeWebPForTest(w io.Writer, img image.Image, q float32) error {
	return webp.Encode(w, img, &webp.EncoderOptions{Quality: q})
}

// DecodeWebPForTest decodes a webp stream.
func DecodeWebPForTest(r io.Reader) (image.Image, error) {
	return webp.Decode(r)
}

// DecodeWebPConfigForTest reads a webp header.
func DecodeWebPConfigForTest(r io.Reader) (image.Config, error) {
	return webp.DecodeConfig(r)
}
