package media

import "encoding/binary"

const (
	markerPrefix     = 0xFF
	markerStuffed    = 0x00
	markerSOI        = 0xD8
	markerSOF0       = 0xC0
	markerSOF1       = 0xC1
	markerSOF2       = 0xC2
	markerRST0       = 0xD0
	markerRST7       = 0xD7
	markerSOS        = 0xDA
	markerEOI        = 0xD9
	markerHeaderSize = 2
	segmentLenSize   = 2
)

// jpegSegments calls visit with each marker segment ahead of a jpeg's first
// scan, stopping early when visit returns false or the stream is malformed.
func jpegSegments(raw []byte, visit func(marker byte, payload []byte) bool) {
	if len(raw) < markerHeaderSize || raw[0] != markerPrefix || raw[1] != markerSOI {
		return
	}
	for i := markerHeaderSize; ; {
		marker, next, ok := nextJPEGMarker(raw, i)
		if !ok || marker == markerSOS || marker == markerEOI || next+segmentLenSize > len(raw) {
			return
		}
		end := next + int(binary.BigEndian.Uint16(raw[next:]))
		if end < next+segmentLenSize || end > len(raw) || !visit(marker, raw[next+segmentLenSize:end]) {
			return
		}
		i = end
	}
}

// nextJPEGMarker returns the first segment marker at or after raw[i] and the
// index just past it. It skips what image/jpeg skips between segments
// (extraneous bytes, stuffed zeros, fill bytes, restart markers), so the
// walker sees the segments the decoder acts on.
func nextJPEGMarker(raw []byte, i int) (byte, int, bool) {
	for i+markerHeaderSize <= len(raw) {
		prev, marker := raw[i], raw[i+1]
		i += markerHeaderSize
		for prev != markerPrefix {
			if i >= len(raw) {
				return 0, 0, false
			}
			prev, marker = marker, raw[i]
			i++
		}
		if marker == markerStuffed {
			continue
		}
		for marker == markerPrefix {
			if i >= len(raw) {
				return 0, 0, false
			}
			marker = raw[i]
			i++
		}
		if marker < markerRST0 || marker > markerRST7 {
			return marker, i, true
		}
	}

	return 0, 0, false
}

// jpegProgressive reports whether raw's frame header is progressive (SOF2).
// A jpeg whose frame header cannot be found reports true, the costlier case.
func jpegProgressive(raw []byte) bool {
	progressive := true
	jpegSegments(raw, func(marker byte, _ []byte) bool {
		switch marker {
		case markerSOF0, markerSOF1, markerSOF2:
			progressive = marker == markerSOF2

			return false
		default:
			return true
		}
	})

	return progressive
}
