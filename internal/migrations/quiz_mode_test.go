package migrations_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestQuizModeMigration_BackfillsSolo pins the MP-0 backfill (#677): a
// quizzes row inserted without a mode lands as 'solo', matching how the
// ADD COLUMN ... DEFAULT 'solo' backfilled the rows that existed before
// the migration. dbtest.Open already ran every migration including this
// one, so a raw INSERT that omits mode exercises the same column default
// the backfill relied on; the store then reads it back as ModeSolo.
func TestQuizModeMigration_BackfillsSolo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	creatorID := seedPlayer(t, db)

	// Insert straight through SQL, omitting mode, so the column DEFAULT
	// fires exactly as it did for pre-migration rows during the backfill.
	res, err := db.ExecContext(
		ctx,
		"INSERT INTO quizzes (title, slug, description, created_by_player_id) VALUES (?, ?, ?, ?)",
		"Legacy quiz", "legacy-quiz", "seeded pre-mode", creatorID,
	)
	if err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	quizID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId err = %v, want nil", err)
	}

	quizStore := store.NewQuizStore(db, slog.Default())
	qz, err := quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("GetQuiz err = %v, want nil", err)
	}
	if got, want := qz.Mode, quiz.ModeSolo; got != want {
		t.Errorf("backfilled quiz mode = %q, want %q", got, want)
	}

	// The CHECK constraint must reject any mode outside ('solo','live').
	if _, err = db.ExecContext(
		ctx,
		"UPDATE quizzes SET mode = 'bogus' WHERE id = ?",
		quizID,
	); err == nil {
		t.Error("update to unknown mode err = nil, want a CHECK violation")
	}
}
