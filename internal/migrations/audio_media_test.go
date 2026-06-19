package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// audioMediaVersion is the #1059 migration adding media.duration_ms and
// questions.audio_media_id.
const audioMediaVersion = 20260618120000

// TestAudioMediaMigration_Columns pins the #1059 schema additions: media gains a
// duration_ms column and questions gains an audio_media_id column with a foreign
// key to media (now in addition to media_id).
func TestAudioMediaMigration_Columns(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "media")["duration_ms"] {
		t.Error("media is missing the duration_ms column")
	}

	if !tableColumns(t, db, "questions")["audio_media_id"] {
		t.Error("questions is missing the audio_media_id column")
	}
	// questions already FK-references media via image_media_id, so a target-set
	// check cannot prove audio_media_id specifically got its own FK; assert the
	// FK keyed on the new column directly.
	if !foreignKeyOnColumn(t, db, "questions", "audio_media_id", "media") {
		t.Error("questions.audio_media_id is missing its foreign key to media")
	}
}

// TestAudioMediaMigration_DownUpRoundTrips pins that the #1059 migration both
// directions cleanly: Down drops media.duration_ms and questions.audio_media_id
// (the latter being the source side of an FK), and Up re-adds them. Exercising
// Down-then-Up against a populated DB catches a broken rebuild the fresh-DB
// dbtest.Open run cannot.
func TestAudioMediaMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	// Seed a quiz + media row so Down/Up runs against populated tables, not an
	// empty schema.
	quizID := seedQuiz(t, db, "Audio roundtrip", "audio-down-up-roundtrip")
	seedMedia(t, db, quizID)

	if err := goose.DownTo(db, ".", audioMediaVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "media")["duration_ms"] {
		t.Error("media still has duration_ms after Down, want it dropped")
	}
	if tableColumns(t, db, "questions")["audio_media_id"] {
		t.Error("questions still has audio_media_id after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "media")["duration_ms"] {
		t.Error("media is missing duration_ms after re-Up")
	}
	if !tableColumns(t, db, "questions")["audio_media_id"] {
		t.Error("questions is missing audio_media_id after re-Up")
	}
	if !foreignKeyOnColumn(t, db, "questions", "audio_media_id", "media") {
		t.Error("questions.audio_media_id is missing its foreign key after re-Up")
	}
}

// foreignKeyOnColumn reports whether the named table has a foreign key whose
// source column is fromCol targeting toTable.
func foreignKeyOnColumn(t *testing.T, db *sql.DB, table, fromCol, toTable string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM pragma_foreign_key_list(?) WHERE "from" = ? AND "table" = ?`,
		table, fromCol, toTable,
	).Scan(&n); err != nil {
		t.Fatalf("pragma_foreign_key_list err = %v, want nil", err)
	}

	return n > 0
}

// TestAudioMediaMigration_DurationRoundTrips pins that duration_ms stores and
// reads back an explicit value, and stays NULL when omitted.
func TestAudioMediaMigration_DurationRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio duration", "audio-duration-roundtrip")
	withDuration := seedMedia(t, db, quizID)
	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET duration_ms = ? WHERE id = ?", 5000, withDuration,
	); err != nil {
		t.Fatalf("set duration_ms err = %v, want nil", err)
	}

	var got sql.NullInt64
	if err := db.QueryRowContext(
		t.Context(), "SELECT duration_ms FROM media WHERE id = ?", withDuration,
	).Scan(&got); err != nil {
		t.Fatalf("read duration_ms err = %v, want nil", err)
	}
	if !got.Valid {
		t.Fatal("duration_ms = NULL, want a stored value")
	}
	if want := int64(5000); got.Int64 != want {
		t.Errorf("duration_ms = %d, want %d", got.Int64, want)
	}

	withoutDuration := seedMedia(t, db, quizID)
	var none sql.NullInt64
	if err := db.QueryRowContext(
		t.Context(), "SELECT duration_ms FROM media WHERE id = ?", withoutDuration,
	).Scan(&none); err != nil {
		t.Fatalf("read default duration_ms err = %v, want nil", err)
	}
	if none.Valid {
		t.Errorf("duration_ms = %d, want NULL when unset", none.Int64)
	}
}

// TestAudioMediaMigration_DeleteSetsNull pins the ON DELETE SET NULL rule on
// audio_media_id (#1059): deleting a sound clears it off any question
// referencing it - the question survives, just loses its audio_media_id.
func TestAudioMediaMigration_DeleteSetsNull(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio question", "audio-media-setnull")
	roundID := seedRound(t, db, quizID)
	mediaID := seedMedia(t, db, quizID)
	questionID := seedQuestionWithAudioMedia(t, db, quizID, roundID, mediaID)

	if _, err := db.ExecContext(
		t.Context(), "DELETE FROM media WHERE id = ?", mediaID,
	); err != nil {
		t.Fatalf("delete media err = %v, want nil", err)
	}

	var got sql.NullInt64
	if err := db.QueryRowContext(
		t.Context(), "SELECT audio_media_id FROM questions WHERE id = ?", questionID,
	).Scan(&got); err != nil {
		t.Fatalf("read question audio_media_id err = %v, want nil (question must survive)", err)
	}
	if got.Valid {
		t.Errorf("question audio_media_id = %d after sound delete, want NULL (ON DELETE SET NULL)", got.Int64)
	}
}

// seedQuestionWithAudioMedia inserts a question attached to mediaID via
// audio_media_id and returns its id.
func seedQuestionWithAudioMedia(t *testing.T, db *sql.DB, quizID, roundID, mediaID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO questions (quiz_id, round_id, text, position, audio_media_id)
		 VALUES (?, ?, 'Q', 1, ?) RETURNING id`,
		quizID, roundID, mediaID,
	).Scan(&id); err != nil {
		t.Fatalf("seed question err = %v, want nil", err)
	}

	return id
}
