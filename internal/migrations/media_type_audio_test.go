package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// mediaTypeAudioPrevVersion is the migration just below the type-unification
// rebuild; DownTo it to land on a schema whose media CHECK still accepts
// 'sound', the legacy value the data migration translates.
const mediaTypeAudioPrevVersion = 20260618120000

// TestMediaTypeAudioMigration_CheckAndShape pins the #1059 kind unification:
// after the rebuild the media type CHECK accepts 'audio' and rejects 'sound',
// both indexes survive, and all 14 columns remain.
func TestMediaTypeAudioMigration_CheckAndShape(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio kind", "media-type-audio-check")
	id := seedMedia(t, db, quizID)

	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET type = 'audio' WHERE id = ?", id,
	); err != nil {
		t.Errorf("UPDATE type = 'audio' err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET type = 'sound' WHERE id = ?", id,
	); err == nil {
		t.Error("UPDATE type = 'sound' err = nil, want a CHECK constraint violation")
	}

	if !indexExists(t, db, "media_quiz_id_idx") {
		t.Error("media_quiz_id_idx index is missing after rebuild")
	}
	if !indexExists(t, db, "media_not_ready_idx") {
		t.Error("media_not_ready_idx index is missing after rebuild")
	}

	wantCols := []string{
		"id", "quiz_id", "type", "mime", "path", "thumb_path", "width",
		"height", "size_bytes", "sha256", "created_by_player_id", "created_at",
		"ready", "duration_ms",
		// description is added later by 20260620120000; dbtest.Open migrates to
		// the latest schema, so it is present here (#1072).
		"description",
		// original_filename is added later by 20260702120000; dbtest.Open
		// migrates to the latest schema, so it is present here (#1137).
		"original_filename",
	}
	gotCols := tableColumns(t, db, "media")
	for _, col := range wantCols {
		if !gotCols[col] {
			t.Errorf("media is missing column %q after rebuild", col)
		}
	}
	if got, want := len(gotCols), len(wantCols); got != want {
		t.Errorf("media column count = %d, want %d (rebuild must not add or drop columns)", got, want)
	}
}

// TestMediaTypeAudioMigration_DataMigration pins that a pre-existing 'sound' row
// is translated to 'audio' by Up. DownTo a schema whose CHECK still allows
// 'sound', insert one, then Up and assert it is now 'audio'.
func TestMediaTypeAudioMigration_DataMigration(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio data", "media-type-audio-data")

	if err := goose.DownTo(db, ".", mediaTypeAudioPrevVersion); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	var id int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO media (quiz_id, type, mime, path, size_bytes, sha256, created_by_player_id)
		 VALUES (?, 'sound', 'audio/mpeg', 'clip.mp3', 10, 'deadbeef', 1) RETURNING id`,
		quizID,
	).Scan(&id); err != nil {
		t.Fatalf("seed sound media err = %v, want nil", err)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	var got string
	if err := db.QueryRowContext(
		t.Context(), "SELECT type FROM media WHERE id = ?", id,
	).Scan(&got); err != nil {
		t.Fatalf("read media type err = %v, want nil", err)
	}
	if want := "audio"; got != want {
		t.Errorf("media type = %q after Up, want %q", got, want)
	}
}

// TestMediaTypeAudioMigration_DownReverts pins the reverse: Down restores the
// legacy CHECK (so 'sound' is accepted again) and translates the canonical
// 'audio' row back to 'sound'.
func TestMediaTypeAudioMigration_DownReverts(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio revert", "media-type-audio-revert")
	id := seedMedia(t, db, quizID)
	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET type = 'audio' WHERE id = ?", id,
	); err != nil {
		t.Fatalf("set type = 'audio' err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", mediaTypeAudioPrevVersion); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	var got string
	if err := db.QueryRowContext(
		t.Context(), "SELECT type FROM media WHERE id = ?", id,
	).Scan(&got); err != nil {
		t.Fatalf("read media type err = %v, want nil", err)
	}
	if want := "sound"; got != want {
		t.Errorf("media type = %q after Down, want %q", got, want)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET type = 'sound' WHERE id = ?", id,
	); err != nil {
		t.Errorf("UPDATE type = 'sound' after Down err = %v, want nil (legacy CHECK)", err)
	}
}
