package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrUnsupportedAudio is returned when an audio upload's magic bytes do not
// match one of the accepted, already-browser-playable formats. Audio is not
// transcoded server-side, so an unrecognised container is rejected rather than
// converted.
var ErrUnsupportedAudio = errors.New("unsupported or unrecognised audio format")

// ErrAudioTooLarge is returned when an audio upload exceeds the configured cap.
var ErrAudioTooLarge = errors.New("audio upload exceeds maximum size")

// Canonical audio MIME types for the accepted formats. The stored MIME is
// derived from the sniffed magic bytes, not the client-declared type.
const (
	mimeMP3 = "audio/mpeg"
	mimeMP4 = "audio/mp4"
	mimeOGG = "audio/ogg"
	mimeWAV = "audio/wav"
)

// File extensions for the accepted audio formats.
const (
	extMP3 = ".mp3"
	extMP4 = ".m4a"
	extOGG = ".ogg"
	extWAV = ".wav"
)

// Magic-byte offsets and the byte length the longest signature check needs. The
// WAV check reads the "RIFF" tag at 0..3 and the "WAVE" form type at 8..11, so
// the sniffer needs at least audioSniffLen bytes for a full match. The ISO-BMFF
// "ftyp" check reads the four-byte major_brand at ftypBrandOffset.
const (
	ftypOffset      = 4
	ftypBrandOffset = 8
	wavFormOffset   = 8
	audioSniffLen   = 12

	// mp3FrameSyncByte is the first byte of an MPEG frame sync; the second byte
	// has its top three bits set, tested with mp3FrameSyncMask.
	mp3FrameSyncByte = 0xFF
	mp3FrameSyncMask = 0xE0
)

// Audio major_brand values an ISO-BMFF "ftyp" box must carry to be accepted as
// audio/mp4. The trailing space is part of the four-byte brand. A video brand
// (e.g. "isom", "mp42", "avc1") is rejected so an MP4 video is not stored as
// audio.
const (
	ftypBrandM4A = "M4A "
	ftypBrandM4B = "M4B "
)

// StoreAudio persists an already-browser-playable audio upload as-is under
// <root>/<quizID>/<id><ext>, recording a media row with the sniffed MIME, byte
// size, sha256, the caller-supplied duration, a description label, and the
// original upload filename (base name, length-capped) as OriginalFilename for the
// library tooltip (#1137). Audio is not decoded or transcoded server-side: the
// format is detected by sniffing magic bytes, and an unrecognised container is
// rejected with ErrUnsupportedAudio rather than converted. durationMs is advisory
// (read in-browser by the caller); a value of zero or less stores NULL.
//
// description is the host-facing library label (#1072). When it is empty it
// defaults to filename without its extension, so a clip always has a readable
// label even on the no-JS upload path; it is editable afterwards.
//
// Like Store, the row is inserted not-ready, the file is written, the path
// recorded, and only then the row is flipped ready (a two-phase commit). A
// failure at any post-insert step tears the row and file back down under a
// cancel-immune context so a failed upload leaves no orphans. Returns
// ErrEmptyUpload for a zero-byte upload and ErrAudioTooLarge when the bytes
// exceed the configured cap.
func (s *Service) StoreAudio(
	ctx context.Context, quizID, createdBy int64, durationMs int, description, filename string, r io.Reader,
) (*Media, error) {
	raw, err := s.readAudioCapped(r)
	if err != nil {
		return nil, err
	}

	mime, ext, ok := sniffAudio(raw)
	if !ok {
		return nil, ErrUnsupportedAudio
	}

	sum := sha256.Sum256(raw)

	row, err := s.store.CreateMedia(ctx, &Media{
		QuizID:            quizID,
		Type:              TypeAudio,
		MIME:              mime,
		SizeBytes:         int64(len(raw)),
		SHA256:            hex.EncodeToString(sum[:]),
		DurationMs:        durationToPtr(durationMs),
		Description:       defaultDescription(description, filename),
		OriginalFilename:  sanitizeFilename(filename),
		CreatedByPlayerID: createdBy,
	})
	if err != nil {
		return nil, fmt.Errorf("creating audio media row: %w", err)
	}

	quizDir := strconv.FormatInt(quizID, decimalBase)
	relPath := filepath.Join(quizDir, strconv.FormatInt(row.ID, decimalBase)+ext)
	// Audio has no thumbnail, so a single blob is committed and ThumbPath stays
	// empty (NULL).
	if err = s.writeAndCommit(ctx, row.ID, quizDir, []fileBlob{{relPath: relPath, data: raw}}); err != nil {
		return nil, err
	}

	row.Path = relPath

	return row, nil
}

// readAudioCapped reads the upload, capping it at audioMaxBytes (plus one byte
// to detect an over-cap upload). A zero or negative cap disables the limit. An
// empty upload is rejected.
func (s *Service) readAudioCapped(r io.Reader) ([]byte, error) {
	reader := r
	if s.audioMaxBytes > 0 {
		// Saturate the read limit rather than computing audioMaxBytes+1: a cap
		// at math.MaxInt64 would overflow to a negative limit, which LimitReader
		// treats as "read nothing" and would fail every upload as empty. At the
		// saturated limit the over-cap check below is unreachable, but a cap that
		// high is effectively no cap.
		limit := int64(math.MaxInt64)
		if s.audioMaxBytes < math.MaxInt64 {
			limit = s.audioMaxBytes + 1
		}
		reader = io.LimitReader(r, limit)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading audio upload: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptyUpload
	}
	if s.audioMaxBytes > 0 && int64(len(raw)) > s.audioMaxBytes {
		return nil, ErrAudioTooLarge
	}

	return raw, nil
}

// maxDescriptionLen bounds a stored description label so a crafted request
// cannot persist an unbounded string (the client maxlength is advisory). Measured
// in runes so a multi-byte label is not cut mid-character. The library/picker
// truncate visually anyway; this is the storage guard.
const maxDescriptionLen = 200

// defaultDescription resolves the stored description label (#1072): a non-empty
// caller-supplied description wins, otherwise it falls back to filename without
// its extension so a clip is never unlabelled even on the no-JS upload path. A
// filename of only an extension (or empty) yields the empty string. The result
// is normalized (trimmed and length-capped) the same way an edit is.
func defaultDescription(description, filename string) string {
	if trimmed := strings.TrimSpace(description); trimmed != "" {
		return normalizeDescription(trimmed)
	}

	base := filepath.Base(filename)

	return normalizeDescription(strings.TrimSuffix(base, filepath.Ext(base)))
}

// normalizeDescription trims surrounding whitespace and caps the label at
// maxDescriptionLen runes, the single normalization both the upload default and
// the inline edit apply so the stored value is bounded and consistent.
func normalizeDescription(description string) string {
	trimmed := strings.TrimSpace(description)
	runes := []rune(trimmed)
	if len(runes) > maxDescriptionLen {
		trimmed = strings.TrimSpace(string(runes[:maxDescriptionLen]))
	}

	return trimmed
}

// durationToPtr maps a caller-supplied duration in milliseconds to the *int the
// media row stores: a value of zero or less (unknown / not measured) becomes
// nil so it is stored NULL rather than as a real zero-length clip.
func durationToPtr(durationMs int) *int {
	if durationMs <= 0 {
		return nil
	}

	return &durationMs
}

// sniffAudio detects an accepted, already-browser-playable audio format from the
// upload's leading bytes and returns its canonical stored MIME and file
// extension. ok is false for an unrecognised format. Detection is by magic
// bytes, not the client-declared type:
//
//   - MP3: "ID3" tag, or an MPEG frame sync (0xFF followed by a byte with its
//     top three bits set).
//   - M4A: an ISO-BMFF "ftyp" box at offset 4 carrying an audio major_brand
//     ("M4A " or "M4B ") at offset 8 (stored as audio/mp4). An MP4 *video*
//     shares the "ftyp" box but carries a video brand, so it is rejected.
//   - OGG: "OggS" header.
//   - WAV: "RIFF" header with the "WAVE" form type at offset 8.
func sniffAudio(b []byte) (mime, ext string, ok bool) {
	switch {
	case isMP3(b):
		return mimeMP3, extMP3, true
	case isAudioMP4(b):
		return mimeMP4, extMP4, true
	case hasPrefixAt(b, 0, "OggS"):
		return mimeOGG, extOGG, true
	case isWAV(b):
		return mimeWAV, extWAV, true
	default:
		return "", "", false
	}
}

// isAudioMP4 reports whether b is an ISO-BMFF "ftyp" container carrying an audio
// major_brand. The "ftyp" box alone is shared by MP4 video, so the major_brand
// at ftypBrandOffset is checked and only audio brands ("M4A " / "M4B ") are
// accepted; a video brand falls through to rejection.
func isAudioMP4(b []byte) bool {
	if !hasPrefixAt(b, ftypOffset, "ftyp") {
		return false
	}

	return hasPrefixAt(b, ftypBrandOffset, ftypBrandM4A) || hasPrefixAt(b, ftypBrandOffset, ftypBrandM4B)
}

// isMP3 reports whether b starts with an MP3 signature: an "ID3" tag or an MPEG
// frame sync (0xFF followed by a byte with its top three bits set).
func isMP3(b []byte) bool {
	if hasPrefixAt(b, 0, "ID3") {
		return true
	}

	return len(b) >= 2 && b[0] == mp3FrameSyncByte && b[1]&mp3FrameSyncMask == mp3FrameSyncMask
}

// isWAV reports whether b is a RIFF/WAVE container: "RIFF" at the start and
// "WAVE" at wavFormOffset.
func isWAV(b []byte) bool {
	return len(b) >= audioSniffLen && hasPrefixAt(b, 0, "RIFF") && hasPrefixAt(b, wavFormOffset, "WAVE")
}

// hasPrefixAt reports whether the four-or-fewer ASCII bytes of sig appear in b
// starting at offset.
func hasPrefixAt(b []byte, offset int, sig string) bool {
	if len(b) < offset+len(sig) {
		return false
	}

	return string(b[offset:offset+len(sig)]) == sig
}
