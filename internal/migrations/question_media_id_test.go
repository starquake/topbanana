package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
)

// TestQuestionMediaIDMigration_ColumnSwap pins the #937 schema swap: questions
// gains a nullable media_id with a foreign key to media, and the legacy
// image_url column is gone.
func TestQuestionMediaIDMigration_ColumnSwap(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	cols := tableColumns(t, db, "questions")
	if !cols["media_id"] {
		t.Error("questions is missing the media_id column")
	}
	if cols["image_url"] {
		t.Error("questions still has the image_url column, want it dropped")
	}

	if !foreignKeyTargets(t, db, "questions")["media"] {
		t.Error("questions is missing a foreign key to media")
	}
}

// TestQuestionMediaIDMigration_DeleteSetsNull pins the ON DELETE SET NULL rule
// (#936 moderation): deleting an image clears it off any question referencing
// it - the question survives, just loses its media_id.
func TestQuestionMediaIDMigration_DeleteSetsNull(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Media question", "media-question-setnull")
	roundID := seedRound(t, db, quizID)
	mediaID := seedMedia(t, db, quizID)
	questionID := seedQuestionWithMedia(t, db, quizID, roundID, mediaID)

	if _, err := db.ExecContext(
		context.Background(), "DELETE FROM media WHERE id = ?", mediaID,
	); err != nil {
		t.Fatalf("delete media err = %v, want nil", err)
	}

	// The question must survive the image delete, with a NULL media_id.
	var got sql.NullInt64
	if err := db.QueryRowContext(
		context.Background(), "SELECT media_id FROM questions WHERE id = ?", questionID,
	).Scan(&got); err != nil {
		t.Fatalf("read question media_id err = %v, want nil (question must survive)", err)
	}
	if got.Valid {
		t.Errorf("question media_id = %d after image delete, want NULL (ON DELETE SET NULL)", got.Int64)
	}
}

// seedQuestionWithMedia inserts a question attached to mediaID and returns its
// id.
func seedQuestionWithMedia(t *testing.T, db *sql.DB, quizID, roundID, mediaID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO questions (quiz_id, round_id, text, position, media_id)
		 VALUES (?, ?, 'Q', 1, ?) RETURNING id`,
		quizID, roundID, mediaID,
	).Scan(&id); err != nil {
		t.Fatalf("seed question err = %v, want nil", err)
	}

	return id
}
