package admin

import (
	"fmt"
	"math"
)

// MediaUploadLimits carries the media upload caps the quiz view shows a host so
// they can avoid picking a file the server will reject (#1139). The numeric byte
// caps also feed the client-side pre-upload size guard (rendered as data
// attributes the upload JS reads); the label methods render the human-readable
// form for the helper text.
type MediaUploadLimits struct {
	// ImageMaxBytes is the per-file image cap in bytes (config MediaImageMaxBytes).
	// Zero means the cap is disabled: the guard is off and no size label shows.
	ImageMaxBytes int64
	// AudioMaxBytes is the per-file audio cap in bytes: the configured audio cap
	// (MediaAudioMaxBytes) clamped down to the largest a single file can be under
	// the multipart request-body cap, so the shown label never exceeds what the
	// server accepts. Zero means disabled, as with ImageMaxBytes.
	AudioMaxBytes int64
	// PerQuizImageLimit is the per-quiz library ceiling per media type (config
	// MediaQuizImageLimit). Zero means the ceiling is disabled.
	PerQuizImageLimit int
}

// ImageMaxLabel is the human-readable per-image size cap (e.g. "10 MB"), or ""
// when the cap is disabled.
func (l MediaUploadLimits) ImageMaxLabel() string { return humanizeBytes(l.ImageMaxBytes) }

// AudioMaxLabel is the human-readable per-clip size cap (e.g. "20 MB"), or ""
// when the cap is disabled.
func (l MediaUploadLimits) AudioMaxLabel() string { return humanizeBytes(l.AudioMaxBytes) }

// humanizeBytes renders a byte count as a short size label for the upload-limit
// helper text, e.g. 10485760 -> "10 MB". It uses binary units (1 MB = 1024 KB)
// to match the caps, which are defined as N<<20, and trims a trailing ".0" so a
// round cap reads "10 MB", not "10.0 MB". A non-positive count yields "".
func humanizeBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const (
		unit = 1024
		// decimalScale rounds the size to one decimal place via the floor below.
		decimalScale = 10
	)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	label := ""
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		label = u
		if value < unit {
			break
		}
	}
	// Floor to one decimal so a cap that is not a clean binary multiple is
	// never rounded UP past the real limit: 9.96 MB must read "9.9 MB", not
	// "10 MB", or a host trusts a size the guard and server would reject.
	value = math.Floor(value*decimalScale) / decimalScale
	if value == math.Trunc(value) {
		return fmt.Sprintf("%d %s", int64(value), label)
	}

	return fmt.Sprintf("%.1f %s", value, label)
}
