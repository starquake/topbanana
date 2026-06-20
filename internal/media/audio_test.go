package media_test

import (
	"bytes"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

// testAudioMaxBytes is a generous audio cap for the shared fixture so the
// accept-path tests are never bounded by it; the over-cap test builds its own
// Service with a tiny cap.
const testAudioMaxBytes int64 = 20 << 20

// mp3ID3 builds a minimal payload whose magic bytes sniff as MP3 via the "ID3"
// tag. The trailing bytes are filler so the row has a non-trivial size.
func mp3ID3() []byte {
	return append([]byte("ID3"), bytes.Repeat([]byte{0x00}, 16)...)
}

// mp3FrameSync builds a minimal payload whose magic bytes sniff as MP3 via the
// MPEG frame sync (0xFF followed by a byte with its top three bits set).
func mp3FrameSync() []byte {
	return append([]byte{0xFF, 0xFB}, bytes.Repeat([]byte{0x00}, 16)...)
}

// m4aFtyp builds a payload whose magic bytes sniff as MP4 audio via "ftyp" at
// offset 4 with the "M4A " audio major_brand at offset 8.
func m4aFtyp() []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypM4A ")...)
}

// m4bFtyp builds a payload sniffing as MP4 audio via the "M4B " audio brand.
func m4bFtyp() []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypM4B ")...)
}

// mp4VideoFtyp builds an ISO-BMFF "ftyp" container carrying a video major_brand
// (brand selectable, e.g. "isom"/"mp42"/"avc1"). It shares the "ftyp" box with
// audio M4A but must be rejected so an MP4 video is not stored as a sound.
func mp4VideoFtyp(brand string) []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftyp"+brand)...)
}

// oggHeader builds a payload whose magic bytes sniff as OGG via "OggS".
func oggHeader() []byte {
	return append([]byte("OggS"), bytes.Repeat([]byte{0x00}, 16)...)
}

// wavHeader builds a payload whose magic bytes sniff as WAV via "RIFF" + "WAVE".
func wavHeader() []byte {
	b := make([]byte, 0, 32)
	b = append(b, []byte("RIFF")...)
	b = append(b, 0x24, 0x00, 0x00, 0x00)
	b = append(b, []byte("WAVE")...)
	b = append(b, bytes.Repeat([]byte{0x00}, 16)...)

	return b
}

// TestSniffAudio pins the magic-byte format detection: each accepted format maps
// to its canonical MIME and extension, and an unrecognised payload is rejected.
func TestSniffAudio(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []byte
		wantMIME string
		wantExt  string
		wantOK   bool
	}{
		{"mp3 id3", mp3ID3(), "audio/mpeg", ".mp3", true},
		{"mp3 frame sync", mp3FrameSync(), "audio/mpeg", ".mp3", true},
		{"m4a ftyp", m4aFtyp(), "audio/mp4", ".m4a", true},
		{"m4b ftyp", m4bFtyp(), "audio/mp4", ".m4a", true},
		{"ogg", oggHeader(), "audio/ogg", ".ogg", true},
		{"wav", wavHeader(), "audio/wav", ".wav", true},
		{"mp4 video isom rejected", mp4VideoFtyp("isom"), "", "", false},
		{"mp4 video mp42 rejected", mp4VideoFtyp("mp42"), "", "", false},
		{"mp4 video avc1 rejected", mp4VideoFtyp("avc1"), "", "", false},
		{"ftyp without brand rejected", []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p'}, "", "", false},
		{"png is not audio", []byte("\x89PNG\r\n\x1a\n"), "", "", false},
		{"random bytes", []byte("not audio at all here"), "", "", false},
		{"too short", []byte{0xFF}, "", "", false},
		{"empty", nil, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotMIME, gotExt, gotOK := ExportSniffAudio(tt.input)
			if gotOK != tt.wantOK {
				t.Fatalf("sniffAudio ok = %t, want %t", gotOK, tt.wantOK)
			}
			if gotMIME != tt.wantMIME {
				t.Errorf("sniffAudio mime = %q, want %q", gotMIME, tt.wantMIME)
			}
			if gotExt != tt.wantExt {
				t.Errorf("sniffAudio ext = %q, want %q", gotExt, tt.wantExt)
			}
		})
	}
}

// TestServiceStoreAudioAcceptsFormats pins the accept path for every supported
// format: StoreAudio records a sound row with the sniffed MIME, writes the
// single file at the returned path, and computes a sha256.
func TestServiceStoreAudioAcceptsFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		payload  []byte
		wantMIME string
		wantExt  string
	}{
		{"mp3 id3", mp3ID3(), "audio/mpeg", ".mp3"},
		{"mp3 frame sync", mp3FrameSync(), "audio/mpeg", ".mp3"},
		{"m4a ftyp", m4aFtyp(), "audio/mp4", ".m4a"},
		{"ogg", oggHeader(), "audio/ogg", ".ogg"},
		{"wav", wavHeader(), "audio/wav", ".wav"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fx := newServiceWithQuiz(t)

			m, err := fx.svc.StoreAudio(
				t.Context(), fx.quizID, seededAdminID, 5000, "", "Theme Song"+tt.wantExt, bytes.NewReader(tt.payload),
			)
			if err != nil {
				t.Fatalf("StoreAudio err = %v, want nil", err)
			}

			if got, want := m.Type, TypeAudio; got != want {
				t.Errorf("Type = %q, want %q", got, want)
			}
			// An empty description defaults to the filename without its extension.
			if got, want := m.Description, "Theme Song"; got != want {
				t.Errorf("Description = %q, want %q", got, want)
			}
			if got, want := m.MIME, tt.wantMIME; got != want {
				t.Errorf("MIME = %q, want %q", got, want)
			}
			if got, want := filepath.Ext(m.Path), tt.wantExt; got != want {
				t.Errorf("path ext = %q, want %q", got, want)
			}
			if got, want := m.SizeBytes, int64(len(tt.payload)); got != want {
				t.Errorf("SizeBytes = %d, want %d", got, want)
			}
			if len(m.SHA256) != 64 {
				t.Errorf("len(SHA256) = %d, want 64 hex chars", len(m.SHA256))
			}
			if m.DurationMs == nil {
				t.Fatal("DurationMs = nil, want 5000")
			}
			if got, want := *m.DurationMs, 5000; got != want {
				t.Errorf("DurationMs = %d, want %d", got, want)
			}

			stat, err := os.Stat(filepath.Join(fx.root, m.Path))
			if err != nil {
				t.Fatalf("stat audio file err = %v, want nil", err)
			}
			if got, want := stat.Size(), m.SizeBytes; got != want {
				t.Errorf("file size = %d, want %d (SizeBytes)", got, want)
			}
		})
	}
}

// TestServiceStoreAudioPersistsDuration pins that the stored duration round-trips
// through the DB (not just the in-memory return), and that a non-positive
// duration stores NULL.
func TestServiceStoreAudioPersistsDuration(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	withDuration, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 3200, "", "clip.mp3", bytes.NewReader(mp3ID3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}
	stored, err := fx.svc.Get(t.Context(), withDuration.ID)
	if err != nil {
		t.Fatalf("Get err = %v, want nil", err)
	}
	if stored.DurationMs == nil {
		t.Fatal("stored DurationMs = nil, want 3200")
	}
	if got, want := *stored.DurationMs, 3200; got != want {
		t.Errorf("stored DurationMs = %d, want %d", got, want)
	}

	noDuration, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 0, "", "clip.ogg", bytes.NewReader(oggHeader()),
	)
	if err != nil {
		t.Fatalf("StoreAudio (no duration) err = %v, want nil", err)
	}
	if noDuration.DurationMs != nil {
		t.Errorf("DurationMs = %d, want nil for a non-positive duration", *noDuration.DurationMs)
	}
	storedNone, err := fx.svc.Get(t.Context(), noDuration.ID)
	if err != nil {
		t.Fatalf("Get (no duration) err = %v, want nil", err)
	}
	if storedNone.DurationMs != nil {
		t.Errorf("stored DurationMs = %d, want nil (NULL)", *storedNone.DurationMs)
	}
}

// TestServiceStoreAudioRejectsUnsupported pins that an unrecognised format is
// rejected with ErrUnsupportedAudio and leaves no row behind.
func TestServiceStoreAudioRejectsUnsupported(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	_, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 1000, "", "clip.mp3",
		bytes.NewReader([]byte("\x89PNG\r\n\x1a\n not audio")),
	)
	if got, want := err, ErrUnsupportedAudio; !errors.Is(got, want) {
		t.Fatalf("StoreAudio err = %v, want %v", got, want)
	}

	rows, err := fx.svc.ListByQuiz(t.Context(), fx.quizID)
	if err != nil {
		t.Fatalf("ListByQuiz err = %v, want nil", err)
	}
	if got, want := len(rows), 0; got != want {
		t.Errorf("rows after rejected upload = %d, want %d", got, want)
	}
}

// TestServiceStoreAudioRejectsVideoMP4 pins that an MP4 *video* (an ISO-BMFF
// "ftyp" container with a video major_brand) is rejected with
// ErrUnsupportedAudio rather than stored as a sound, and leaves no row behind.
func TestServiceStoreAudioRejectsVideoMP4(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	_, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 1000, "", "movie.mp4", bytes.NewReader(mp4VideoFtyp("isom")),
	)
	if got, want := err, ErrUnsupportedAudio; !errors.Is(got, want) {
		t.Fatalf("StoreAudio err = %v, want %v", got, want)
	}

	rows, err := fx.svc.ListByQuiz(t.Context(), fx.quizID)
	if err != nil {
		t.Fatalf("ListByQuiz err = %v, want nil", err)
	}
	if got, want := len(rows), 0; got != want {
		t.Errorf("rows after rejected video upload = %d, want %d", got, want)
	}
}

// TestServiceStoreAudioRejectsEmpty pins that a zero-byte upload is rejected with
// ErrEmptyUpload.
func TestServiceStoreAudioRejectsEmpty(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	_, err := fx.svc.StoreAudio(t.Context(), fx.quizID, seededAdminID, 0, "", "clip.mp3", bytes.NewReader(nil))
	if got, want := err, ErrEmptyUpload; !errors.Is(got, want) {
		t.Errorf("StoreAudio err = %v, want %v", got, want)
	}
}

// TestServiceStoreAudioRejectsOverCap pins that an upload exceeding the
// configured cap is rejected with ErrAudioTooLarge. The Service is built with a
// tiny cap and fed a valid-magic payload longer than it.
func TestServiceStoreAudioRejectsOverCap(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "media-svc-audio-overcap")
	root := t.TempDir()
	const tinyCap int64 = 8
	svc := NewService(store.NewMediaStore(db, slog.Default()), root, testImageMaxBytes, tinyCap, slog.Default())

	payload := append([]byte("ID3"), bytes.Repeat([]byte{0x00}, 64)...)
	_, err := svc.StoreAudio(t.Context(), quizID, seededAdminID, 1000, "", "clip.mp3", bytes.NewReader(payload))
	if got, want := err, ErrAudioTooLarge; !errors.Is(got, want) {
		t.Errorf("StoreAudio err = %v, want %v", got, want)
	}
}

// TestServiceStoreAudioHugeCap pins that a cap near math.MaxInt64 does not
// overflow the LimitReader bound: the +1 would wrap negative and make
// LimitReader report EOF immediately, failing every upload as empty. The store
// must still accept a normal payload at such a cap.
func TestServiceStoreAudioHugeCap(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "media-svc-audio-hugecap")
	root := t.TempDir()
	svc := NewService(store.NewMediaStore(db, slog.Default()), root, testImageMaxBytes, math.MaxInt64, slog.Default())

	m, err := svc.StoreAudio(t.Context(), quizID, seededAdminID, 1000, "", "clip.mp3", bytes.NewReader(mp3ID3()))
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}
	if got, want := m.SizeBytes, int64(len(mp3ID3())); got != want {
		t.Errorf("SizeBytes = %d, want %d", got, want)
	}
}

// TestDefaultDescription pins the description-defaulting rule (#1072): a non-empty
// caller value wins (trimmed), otherwise the filename without its extension is
// used, and a name that is only an extension (or empty) yields the empty string.
func TestDefaultDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		filename    string
		want        string
	}{
		{"explicit wins over filename", "My Clip", "intro.mp3", "My Clip"},
		{"explicit is trimmed", "  spaced  ", "intro.mp3", "spaced"},
		{"blank explicit falls back to filename", "   ", "intro.mp3", "intro"},
		{"empty explicit falls back to filename", "", "Theme Song.ogg", "Theme Song"},
		{"filename without extension", "", "loop", "loop"},
		{"filename strips only last extension", "", "a.b.wav", "a.b"},
		{"filename with directory uses base", "", "/tmp/sounds/win.m4a", "win"},
		{"only an extension yields empty", "", ".mp3", ""},
		{"empty filename yields empty", "", "", ""},
		{"over-long description is capped to 200 runes", strings.Repeat("a", 250), "x.mp3", strings.Repeat("a", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := ExportDefaultDescription(tt.description, tt.filename), tt.want; got != want {
				t.Errorf("defaultDescription(%q, %q) = %q, want %q", tt.description, tt.filename, got, want)
			}
		})
	}
}

// TestServiceStoreAudioExplicitDescription pins that an explicit description is
// stored as-is (trimmed) and round-trips through the DB, rather than being
// overridden by the filename default (#1072).
func TestServiceStoreAudioExplicitDescription(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 0, "  Winning fanfare  ", "fanfare.mp3", bytes.NewReader(mp3ID3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}
	if got, want := m.Description, "Winning fanfare"; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}

	stored, err := fx.svc.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("Get err = %v, want nil", err)
	}
	if got, want := stored.Description, "Winning fanfare"; got != want {
		t.Errorf("stored Description = %q, want %q", got, want)
	}
}

// TestServiceUpdateDescription pins that UpdateDescription trims and persists a
// new label, and that a missing id maps to ErrMediaNotFound.
func TestServiceUpdateDescription(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 0, "first", "first.mp3", bytes.NewReader(mp3ID3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}

	if err = fx.svc.UpdateDescription(t.Context(), m.ID, "  second label  "); err != nil {
		t.Fatalf("UpdateDescription err = %v, want nil", err)
	}
	stored, err := fx.svc.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("Get err = %v, want nil", err)
	}
	if got, want := stored.Description, "second label"; got != want {
		t.Errorf("stored Description = %q, want %q", got, want)
	}

	const missingID int64 = 999999
	if got, want := fx.svc.UpdateDescription(t.Context(), missingID, "x"), ErrMediaNotFound; !errors.Is(got, want) {
		t.Errorf("UpdateDescription(missing) err = %v, want %v", got, want)
	}
}

// TestServiceUpdateDescriptionCapsLength pins that an over-long description is
// capped server-side (the client maxlength is advisory), so a crafted request
// cannot persist an unbounded label (#1072).
func TestServiceUpdateDescriptionCapsLength(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.StoreAudio(
		t.Context(), fx.quizID, seededAdminID, 0, "label", "label.mp3", bytes.NewReader(mp3ID3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}

	if err = fx.svc.UpdateDescription(t.Context(), m.ID, strings.Repeat("z", 500)); err != nil {
		t.Fatalf("UpdateDescription err = %v, want nil", err)
	}
	stored, err := fx.svc.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("Get err = %v, want nil", err)
	}
	if got, want := len([]rune(stored.Description)), 200; got != want {
		t.Errorf("stored Description rune length = %d, want %d (capped)", got, want)
	}
}
