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

// ExportSniffAudio re-exports the unexported audio format sniffer for tests.
var ExportSniffAudio = sniffAudio

// ExportDefaultDescription re-exports the unexported description-defaulting
// helper for tests.
var ExportDefaultDescription = defaultDescription

// ExportSanitizeFilename re-exports the unexported upload-filename sanitizer for
// tests.
var ExportSanitizeFilename = sanitizeFilename

// FillDecodeSlotsForTest occupies every decode slot and returns the func that
// frees them.
func FillDecodeSlotsForTest() func() {
	for range cap(decodeSlots) {
		decodeSlots <- struct{}{}
	}

	return func() {
		for range cap(decodeSlots) {
			<-decodeSlots
		}
	}
}
