package store_test

import (
	"errors"
	"log/slog"
	"testing"

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

// TestMediaStore_CountByQuiz pins CountMediaByQuiz tracks inserts and deletes.
func TestMediaStore_CountByQuiz(t *testing.T) {
	t.Parallel()

	s, quizID := newMediaStoreWithQuiz(t)

	if got, want := countOrFatal(t, s, quizID), int64(0); got != want {
		t.Errorf("initial count = %d, want %d", got, want)
	}

	created, err := s.CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	if got, want := countOrFatal(t, s, quizID), int64(1); got != want {
		t.Errorf("count after insert = %d, want %d", got, want)
	}

	if err = s.DeleteMedia(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteMedia err = %v, want nil", err)
	}
	if got, want := countOrFatal(t, s, quizID), int64(0); got != want {
		t.Errorf("count after delete = %d, want %d", got, want)
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

func countOrFatal(t *testing.T, s *MediaStore, quizID int64) int64 {
	t.Helper()
	n, err := s.CountMediaByQuiz(t.Context(), quizID)
	if err != nil {
		t.Fatalf("CountMediaByQuiz err = %v, want nil", err)
	}

	return n
}
