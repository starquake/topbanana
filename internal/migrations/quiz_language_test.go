package migrations_test

import (
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestQuizLanguageMigration_DefaultsToEnglish pins the #1115 migration: a
// quizzes row inserted without a language lands as 'en', the CHECK rejects any
// value outside ('en','nl'), and a valid 'nl' round-trips through the store.
func TestQuizLanguageMigration_DefaultsToEnglish(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	creatorID := seedPlayer(t, db)

	// Insert through raw SQL omitting language, so the column DEFAULT fires as
	// it did for pre-migration rows during the backfill.
	res, err := db.ExecContext(
		ctx,
		"INSERT INTO quizzes (title, slug, description, created_by_player_id) VALUES (?, ?, ?, ?)",
		"Legacy quiz", "legacy-quiz-lang", "seeded pre-language", creatorID,
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
	if got, want := qz.Language, quiz.LanguageEN; got != want {
		t.Errorf("backfilled quiz language = %q, want %q", got, want)
	}

	// A valid 'nl' round-trips.
	if _, err = db.ExecContext(
		ctx, "UPDATE quizzes SET language = 'nl' WHERE id = ?", quizID,
	); err != nil {
		t.Fatalf("update to nl err = %v, want nil", err)
	}
	qz, err = quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("GetQuiz after nl err = %v, want nil", err)
	}
	if got, want := qz.Language, quiz.LanguageNL; got != want {
		t.Errorf("quiz language after update = %q, want %q", got, want)
	}

	// The CHECK constraint must reject any language outside ('en','nl').
	if _, err = db.ExecContext(
		ctx, "UPDATE quizzes SET language = 'bogus' WHERE id = ?", quizID,
	); err == nil {
		t.Error("update to unknown language err = nil, want a CHECK violation")
	}
}
