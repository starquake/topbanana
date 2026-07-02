package admin_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/admin"
)

// TestMediaUploadLimitsImageLabel drives the byte-to-label formatting through
// the exported ImageMaxLabel method (the humanizeBytes helper it wraps is
// unexported).
func TestMediaUploadLimitsImageLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int64
		want string
	}{
		{"zero disables", 0, ""},
		{"negative disables", -1, ""},
		{"sub-kilobyte bytes", 512, "512 B"},
		{"round kilobytes", 4 << 10, "4 KB"},
		{"round megabytes", 10 << 20, "10 MB"},
		{"round gigabytes", 3 << 30, "3 GB"},
		{"fractional megabytes", (10 << 20) + (512 << 10), "10.5 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := admin.MediaUploadLimits{ImageMaxBytes: tt.in}.ImageMaxLabel()
			if want := tt.want; got != want {
				t.Errorf("ImageMaxLabel() for %d bytes = %q, want %q", tt.in, got, want)
			}
		})
	}
}

func TestMediaUploadLimitsFields(t *testing.T) {
	t.Parallel()

	limits := admin.MediaUploadLimits{
		ImageMaxBytes:     10 << 20,
		AudioMaxBytes:     20 << 20,
		MaxFilesPerBatch:  10,
		PerQuizImageLimit: 200,
	}

	if got, want := limits.ImageMaxLabel(), "10 MB"; got != want {
		t.Errorf("ImageMaxLabel() = %q, want %q", got, want)
	}
	if got, want := limits.AudioMaxLabel(), "20 MB"; got != want {
		t.Errorf("AudioMaxLabel() = %q, want %q", got, want)
	}
	if got, want := limits.MaxFilesPerBatch, 10; got != want {
		t.Errorf("MaxFilesPerBatch = %d, want %d", got, want)
	}
	if got, want := limits.PerQuizImageLimit, 200; got != want {
		t.Errorf("PerQuizImageLimit = %d, want %d", got, want)
	}
}

func TestMediaUploadLimitsAudioLabelDisabled(t *testing.T) {
	t.Parallel()

	limits := admin.MediaUploadLimits{ImageMaxBytes: 10 << 20, MaxFilesPerBatch: 10, PerQuizImageLimit: 200}
	if got, want := limits.AudioMaxLabel(), ""; got != want {
		t.Errorf("AudioMaxLabel() with disabled cap = %q, want %q", got, want)
	}
}
