package media

import (
	"image"
	"io"

	"github.com/deepteams/webp"
)

// The *ForTest wrappers route the external media_test package's webp codec calls
// through webpMu, so the tests serialize on the same lock production does. The
// deepteams/webp codec shares unsynchronized package state, so a test that calls
// webp.Encode/Decode directly would race a concurrent Process under -race even
// though production is serialized. See webpMu.

// EncodeWebPForTest encodes img at quality q under webpMu.
func EncodeWebPForTest(w io.Writer, img image.Image, q float32) error {
	webpMu.Lock()
	defer webpMu.Unlock()

	return webp.Encode(w, img, &webp.EncoderOptions{Quality: q})
}

// DecodeWebPForTest decodes a webp stream under webpMu.
func DecodeWebPForTest(r io.Reader) (image.Image, error) {
	webpMu.Lock()
	defer webpMu.Unlock()

	return webp.Decode(r)
}

// DecodeWebPConfigForTest reads a webp header under webpMu.
func DecodeWebPConfigForTest(r io.Reader) (image.Config, error) {
	webpMu.Lock()
	defer webpMu.Unlock()

	return webp.DecodeConfig(r)
}
