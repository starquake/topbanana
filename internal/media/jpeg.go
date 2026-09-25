package media

import "encoding/binary"

const (
	markerPrefix     = 0xFF
	markerSOI        = 0xD8
	markerSOF2       = 0xC2
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
	for i := markerHeaderSize; i+markerHeaderSize+segmentLenSize <= len(raw); {
		if raw[i] != markerPrefix {
			return
		}
		marker := raw[i+1]
		if marker == markerPrefix {
			i++

			continue
		}
		if marker == markerSOS || marker == markerEOI {
			return
		}
		segLen := int(binary.BigEndian.Uint16(raw[i+markerHeaderSize:]))
		end := i + markerHeaderSize + segLen
		if segLen < segmentLenSize || end > len(raw) {
			return
		}
		if !visit(marker, raw[i+markerHeaderSize+segmentLenSize:end]) {
			return
		}
		i = end
	}
}

// jpegProgressive reports whether raw is a progressive jpeg (SOF2).
func jpegProgressive(raw []byte) bool {
	progressive := false
	jpegSegments(raw, func(marker byte, _ []byte) bool {
		progressive = marker == markerSOF2

		return !progressive
	})

	return progressive
}
