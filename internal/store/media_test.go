package store_test

import (
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/media"
	. "github.com/starquake/topbanana/internal/store"
)

// newMediaStoreWithQuiz opens a migrated DB, seeds a quiz, and returns a
// MediaStore plus the quiz id. Skips under -short via dbtest.Open.
func newMediaStoreWithQuiz(t *testing.T) (*MediaStore, int64) {
	t.Helper()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	var quizID int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO quizzes (title, slug, description, created_by_player_id)
		 VALUES ('Media', 'media-store', 'd', 1) RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}

	return NewMediaStore(db, slog.Default()), quizID
}

func newMediaRow(quizID int64) *media.Media {
	return &media.Media{
		QuizID:            quizID,
		Type:              media.TypeImage,
		MIME:              "image/jpeg",
		Path:              "p.jpg",
		ThumbPath:         "p-thumb.jpg",
		Width:             640,
		Height:            480,
		SizeBytes:         1234,
		SHA256:            "deadbeef",
		CreatedByPlayerID: seededAdminID,
	}
}

func newAudioMediaRow(quizID int64) *media.Media {
	durationMs := 1500

	return &media.Media{
		QuizID:            quizID,
		Type:              media.TypeAudio,
		MIME:              "audio/mpeg",
		Path:              "a.mp3",
		SizeBytes:         2048,
		SHA256:            "cafebabe",
		DurationMs:        &durationMs,
		CreatedByPlayerID: seededAdminID,
	}
}

// TestMediaStore_CreateGetRoundTrip pins that a created row reads back with the
// same metadata, with nullable width/height and thumb_path surviving the
// NULL-mapping round trip.
func TestMediaStore_CreateGetRoundTrip(t *testing.T) {
	t.Parallel()

	s, quizID := newMediaStoreWithQuiz(t)

	created, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	if created.ID == 0 {
		t.Error("created ID = 0, want assigned id")
	}
	if created.CreatedAt.IsZero() {
		t.Error("created CreatedAt is zero, want default timestamp")
	}

	got, err := s.GetMedia(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMedia err = %v, want nil", err)
	}
	if got, want := got.Width, 640; got != want {
		t.Errorf("Width = %d, want %d", got, want)
	}
	if got, want := got.Height, 480; got != want {
		t.Errorf("Height = %d, want %d", got, want)
	}
	if got, want := got.ThumbPath, "p-thumb.jpg"; got != want {
		t.Errorf("ThumbPath = %q, want %q", got, want)
	}
	if got, want := got.SHA256, "deadbeef"; got != want {
		t.Errorf("SHA256 = %q, want %q", got, want)
	}
}

// TestMediaStore_GetMissing pins the not-found mapping: an unknown id maps to
// media.ErrMediaNotFound, not a generic sql error.
func TestMediaStore_GetMissing(t *testing.T) {
	t.Parallel()

	s, _ := newMediaStoreWithQuiz(t)

	if _, err := s.GetMedia(t.Context(), 999); !errors.Is(err, media.ErrMediaNotFound) {
		t.Errorf("GetMedia(missing) err = %v, want ErrMediaNotFound", err)
	}
}

// TestMediaStore_DeleteMissing pins that deleting an unknown id maps to
// media.ErrMediaNotFound via the RowsAffected check.
func TestMediaStore_DeleteMissing(t *testing.T) {
	t.Parallel()

	s, _ := newMediaStoreWithQuiz(t)

	if err := s.DeleteMedia(t.Context(), 999); !errors.Is(err, media.ErrMediaNotFound) {
		t.Errorf("DeleteMedia(missing) err = %v, want ErrMediaNotFound", err)
	}
}

// TestMediaStore_UpdatePathsMissing pins that updating paths on an unknown id
// maps to media.ErrMediaNotFound.
func TestMediaStore_UpdatePathsMissing(t *testing.T) {
	t.Parallel()

	s, _ := newMediaStoreWithQuiz(t)

	if err := s.UpdateMediaPaths(t.Context(), 999, "a.jpg", "a-thumb.jpg"); !errors.Is(
		err, media.ErrMediaNotFound,
	) {
		t.Errorf("UpdateMediaPaths(missing) err = %v, want ErrMediaNotFound", err)
	}
}

// TestMediaStore_CountByQuizAndType pins CountMediaByQuizAndType tracks ready
// inserts and deletes. A freshly created (not-ready) row does not count until
// MarkMediaReady flips it, so a cancelled upload never inflates the per-quiz cap
// (#992).
func TestMediaStore_CountByQuizAndType(t *testing.T) {
	t.Parallel()

	s, quizID := newMediaStoreWithQuiz(t)

	if got, want := countOrFatal(t, s, quizID, media.TypeImage), int64(0); got != want {
		t.Errorf("initial count = %d, want %d", got, want)
	}

	created, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	if got, want := countOrFatal(t, s, quizID, media.TypeImage), int64(0); got != want {
		t.Errorf("count after not-ready insert = %d, want %d (not-ready rows excluded)", got, want)
	}

	if err = s.MarkMediaReady(t.Context(), created.ID); err != nil {
		t.Fatalf("MarkMediaReady err = %v, want nil", err)
	}
	if got, want := countOrFatal(t, s, quizID, media.TypeImage), int64(1); got != want {
		t.Errorf("count after mark ready = %d, want %d", got, want)
	}

	if err = s.DeleteMedia(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteMedia err = %v, want nil", err)
	}
	if got, want := countOrFatal(t, s, quizID, media.TypeImage), int64(0); got != want {
		t.Errorf("count after delete = %d, want %d", got, want)
	}
}

// TestMediaStore_CountByQuizAndTypeIsTypeScoped pins that the per-type count
// keeps image and audio ceilings independent: an audio row does not inflate the
// image count and vice versa (#1059).
func TestMediaStore_CountByQuizAndTypeIsTypeScoped(t *testing.T) {
	t.Parallel()

	s, quizID := newMediaStoreWithQuiz(t)

	markReady := func(row *media.Media) {
		t.Helper()
		created, err := s.CreateMedia(t.Context(), row)
		if err != nil {
			t.Fatalf("CreateMedia err = %v, want nil", err)
		}
		if err = s.MarkMediaReady(t.Context(), created.ID); err != nil {
			t.Fatalf("MarkMediaReady err = %v, want nil", err)
		}
	}

	markReady(newMediaRow(quizID))
	markReady(newAudioMediaRow(quizID))
	markReady(newAudioMediaRow(quizID))

	if got, want := countOrFatal(t, s, quizID, media.TypeImage), int64(1); got != want {
		t.Errorf("image count = %d, want %d (audio rows excluded)", got, want)
	}
	if got, want := countOrFatal(t, s, quizID, media.TypeAudio), int64(2); got != want {
		t.Errorf("audio count = %d, want %d (image row excluded)", got, want)
	}
}

// TestMediaStore_ListExcludesNotReady pins that ListMediaByQuiz hides a
// not-ready row and surfaces it once MarkMediaReady flips it, so a cancelled
// upload (committed but never marked ready) never shows in the library (#992).
func TestMediaStore_ListExcludesNotReady(t *testing.T) {
	t.Parallel()

	s, quizID := newMediaStoreWithQuiz(t)

	created, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}

	list, err := s.ListMediaByQuiz(t.Context(), quizID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}
	if got, want := len(list), 0; got != want {
		t.Errorf("list before mark ready = %d rows, want %d (not-ready hidden)", got, want)
	}

	if err = s.MarkMediaReady(t.Context(), created.ID); err != nil {
		t.Fatalf("MarkMediaReady err = %v, want nil", err)
	}

	list, err = s.ListMediaByQuiz(t.Context(), quizID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}
	if got, want := len(list), 1; got != want {
		t.Fatalf("list after mark ready = %d rows, want %d", got, want)
	}
	if got, want := list[0].ID, created.ID; got != want {
		t.Errorf("list[0].ID = %d, want %d", got, want)
	}
}

// TestMediaStore_MarkReadyMissing pins that marking an unknown id ready maps to
// media.ErrMediaNotFound via the RowsAffected check.
func TestMediaStore_MarkReadyMissing(t *testing.T) {
	t.Parallel()

	s, _ := newMediaStoreWithQuiz(t)

	if err := s.MarkMediaReady(t.Context(), 999); !errors.Is(err, media.ErrMediaNotFound) {
		t.Errorf("MarkMediaReady(missing) err = %v, want ErrMediaNotFound", err)
	}
}

// TestMediaStore_ListStaleNotReady pins the sweep query: a not-ready row older
// than the cutoff is returned with its paths, a ready row of the same age never
// is, and a just-minted not-ready row is not yet stale (#992). The stale and
// ready rows are backdated via direct SQL so the test does not have to sleep.
func TestMediaStore_ListStaleNotReady(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})
	var quizID int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO quizzes (title, slug, description, created_by_player_id)
		 VALUES ('Media', 'media-stale', 'd', 1) RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	s := NewMediaStore(db, slog.Default())

	row := newMediaRow(quizID)
	stale, err := s.CreateMedia(t.Context(), row)
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	ready, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia (ready) err = %v, want nil", err)
	}
	if err = s.MarkMediaReady(t.Context(), ready.ID); err != nil {
		t.Fatalf("MarkMediaReady err = %v, want nil", err)
	}
	young, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia (young) err = %v, want nil", err)
	}

	backdateMedia(t, db, stale.ID)
	backdateMedia(t, db, ready.ID)

	// A one-minute window: the backdated rows (an hour old) are past it; the
	// just-minted young not-ready row is not.
	list, err := s.ListStaleNotReadyMedia(t.Context(), time.Minute)
	if err != nil {
		t.Fatalf("ListStaleNotReadyMedia err = %v, want nil", err)
	}
	if got, want := len(list), 1; got != want {
		t.Fatalf("stale list = %d rows, want %d (only the backdated not-ready row)", got, want)
	}
	if got, want := list[0].ID, stale.ID; got != want {
		t.Errorf("stale[0].ID = %d, want %d", got, want)
	}
	if got, want := list[0].Path, row.Path; got != want {
		t.Errorf("stale[0].Path = %q, want %q", got, want)
	}
	if list[0].ID == young.ID {
		t.Error("young not-ready row was reported stale, want it excluded")
	}
}

// backdateMedia rolls a media row's created_at back an hour via direct SQL so a
// staleness test does not have to sleep.
func backdateMedia(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.ExecContext(
		t.Context(),
		"UPDATE media SET created_at = datetime('now', '-1 hour') WHERE id = ?", id,
	); err != nil {
		t.Fatalf("backdate media err = %v, want nil", err)
	}
}

// TestMediaStore_CreateRejectsUnknownQuiz pins the quiz_id foreign key: a row
// referencing a non-existent quiz is rejected by the DB.
func TestMediaStore_CreateRejectsUnknownQuiz(t *testing.T) {
	t.Parallel()

	s, _ := newMediaStoreWithQuiz(t)

	if _, err := s.CreateMedia(t.Context(), newMediaRow(999_999)); err == nil {
		t.Error("CreateMedia with unknown quiz err = nil, want a foreign-key violation")
	}
}

func countOrFatal(t *testing.T, s *MediaStore, quizID int64, mediaType string) int64 {
	t.Helper()
	n, err := s.CountMediaByQuizAndType(t.Context(), quizID, mediaType)
	if err != nil {
		t.Fatalf("CountMediaByQuizAndType err = %v, want nil", err)
	}

	return n
}
