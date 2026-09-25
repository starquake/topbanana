package media_test

import (
	"bytes"
	"slices"
	"testing"

	. "github.com/starquake/topbanana/internal/media"
)

// jpegStream joins an SOI marker and parts into one jpeg byte stream.
func jpegStream(parts ...[]byte) []byte {
	return slices.Concat(append([][]byte{{0xFF, 0xD8}}, parts...)...)
}

// seg builds a marker segment with a length field covering payload.
func seg(marker byte, payload ...byte) []byte {
	n := len(payload) + 2

	return append([]byte{0xFF, marker, byte(n >> 8), byte(n)}, payload...)
}

func TestJPEGSegments(t *testing.T) {
	t.Parallel()

	app0 := seg(0xE0, 'J', 'F', 'I', 'F', 0)
	sof2 := seg(0xC2, 8, 0, 1, 0, 1, 1, 1, 0x11, 0)
	cases := map[string]struct {
		raw  []byte
		want []byte
	}{
		"segments in order":      {jpegStream(app0, sof2), []byte{0xE0, 0xC2}},
		"junk byte skipped":      {jpegStream(app0, []byte{0x42, 0x43}, sof2), []byte{0xE0, 0xC2}},
		"stuffed zero skipped":   {jpegStream(app0, []byte{0xFF, 0x00}, sof2), []byte{0xE0, 0xC2}},
		"restart marker skipped": {jpegStream(app0, []byte{0xFF, 0xD7}, sof2), []byte{0xE0, 0xC2}},
		"fill bytes skipped":     {jpegStream(app0, []byte{0xFF, 0xFF}, sof2), []byte{0xE0, 0xC2}},
		"stops at SOS":           {jpegStream(app0, seg(0xDA, 1), sof2), []byte{0xE0}},
		"stops at EOI":           {jpegStream(app0, []byte{0xFF, 0xD9}, sof2), []byte{0xE0}},
		"stops at truncation":    {jpegStream(app0, sof2[:len(sof2)-1]), []byte{0xE0}},
		"stops at short length":  {jpegStream([]byte{0xFF, 0xE1, 0, 1}, sof2), nil},
		"not a jpeg":             {[]byte("\x89PNG\r\n\x1a\n"), nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []byte
			ExportJPEGSegments(tc.raw, func(marker byte, _ []byte) bool {
				got = append(got, marker)

				return true
			})
			if want := tc.want; !bytes.Equal(got, want) {
				t.Errorf("visited markers = % X, want % X", got, want)
			}
		})
	}
}

func TestJPEGSegmentsPayload(t *testing.T) {
	t.Parallel()

	raw := jpegStream([]byte{0x42}, seg(0xE1, 'a', 'b', 'c'))
	var got []byte
	ExportJPEGSegments(raw, func(_ byte, payload []byte) bool {
		got = payload

		return false
	})
	if got, want := string(got), "abc"; got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestJPEGProgressive(t *testing.T) {
	t.Parallel()

	frame := []byte{8, 0, 1, 0, 1, 1, 1, 0x11, 0}
	app0 := seg(0xE0, 'J', 'F', 'I', 'F', 0)
	cases := map[string]struct {
		raw  []byte
		want bool
	}{
		"baseline":                   {jpegStream(app0, seg(0xC0, frame...)), false},
		"extended sequential":        {jpegStream(app0, seg(0xC1, frame...)), false},
		"progressive":                {jpegStream(app0, seg(0xC2, frame...)), true},
		"progressive after junk":     {jpegStream(app0, []byte{0x42}, seg(0xC2, frame...)), true},
		"progressive after stuffed":  {jpegStream(app0, []byte{0xFF, 0x00}, seg(0xC2, frame...)), true},
		"progressive after RST0":     {jpegStream(app0, []byte{0xFF, 0xD0}, seg(0xC2, frame...)), true},
		"first frame wins":           {jpegStream(seg(0xC0, frame...), seg(0xC2, frame...)), false},
		"no frame before SOS":        {jpegStream(app0, seg(0xDA, 1), seg(0xC0, frame...)), true},
		"truncated before the frame": {jpegStream(app0, seg(0xC0, frame...)[:5]), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, want := ExportJPEGProgressive(tc.raw), tc.want; got != want {
				t.Errorf("jpegProgressive = %t, want %t", got, want)
			}
		})
	}
}
