package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// quizPlayCountVersion is the #891 ADD COLUMN + backfill migration. Down-to one
// version below, seed games, re-Up to exercise the backfill against populated
// data the way dbtest.Open's fresh-DB run cannot.
const quizPlayCountVersion = 20260614120000

// TestQuizPlayCountMigration_BackfillsFromCompletedSoloGames pins the #891 seed:
// the migration sets quizzes.play_count to the count of "completed" games per
// quiz (every quiz question issued via game_questions), matching the finisher
// predicate ListPopularQuizzes uses. An in-progress game with fewer issued
// questions than the quiz holds is not counted; a quiz with no completed games
// lands at zero.
func TestQuizPlayCountMigration_BackfillsFromCompletedSoloGames(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if err := goose.DownTo(db, ".", quizPlayCountVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	popularID := seedQuiz(t, db, "Popular", "popular-pc-mig")
	loneID := seedQuiz(t, db, "Lone", "lone-pc-mig")
	emptyID := seedQuiz(t, db, "Empty", "empty-pc-mig")

	popularRoundID := seedRound(t, db, popularID)
	popularQ1 := seedQuestion(t, db, popularID, popularRoundID, 1)
	popularQ2 := seedQuestion(t, db, popularID, popularRoundID, 2)
	loneRoundID := seedRound(t, db, loneID)
	loneQ1 := seedQuestion(t, db, loneID, loneRoundID, 1)

	// Two completed games on the popular quiz (every quiz question issued).
	seedCompletedGame(t, db, "game-pop-1", popularID, []int64{popularQ1, popularQ2})
	seedCompletedGame(t, db, "game-pop-2", popularID, []int64{popularQ1, popularQ2})
	// An in-progress game on the popular quiz (only one of two questions issued)
	// must NOT count.
	seedCompletedGame(t, db, "game-pop-3", popularID, []int64{popularQ1})
	// One completed game on the lone quiz; the empty quiz gets none.
	seedCompletedGame(t, db, "game-lone-1", loneID, []int64{loneQ1})

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	if got, want := readPlayCount(t, db, popularID), int64(2); got != want {
		t.Errorf("popular quiz play_count = %d, want %d (two completed games)", got, want)
	}
	if got, want := readPlayCount(t, db, loneID), int64(1); got != want {
		t.Errorf("lone quiz play_count = %d, want %d (one completed game)", got, want)
	}
	if got, want := readPlayCount(t, db, emptyID), int64(0); got != want {
		t.Errorf("empty quiz play_count = %d, want %d (no games)", got, want)
	}
}

// TestQuizPlayCountMigration_CheckConstraintRejectsNegative pins the CHECK on
// play_count: the column refuses a write that would push the counter negative,
// so a mis-written UPDATE cannot poison the durable hit counter.
func TestQuizPlayCountMigration_CheckConstraintRejectsNegative(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Check", "check-pc-mig")

	_, err := db.ExecContext(
		context.Background(), "UPDATE quizzes SET play_count = -1 WHERE id = ?", quizID,
	)
	if err == nil {
		t.Error("UPDATE to -1 err = nil, want a CHECK violation")
	}
}

func seedQuiz(t *testing.T, db *sql.DB, title, slug string) int64 {
	t.Helper()
	var quizID int64
	if err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO quizzes (title, slug, description, created_by_player_id)
		 VALUES (?, ?, 'd', 1) RETURNING id`,
		title, slug,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz %q err = %v, want nil", slug, err)
	}

	return quizID
}

func seedRound(t *testing.T, db *sql.DB, quizID int64) int64 {
	t.Helper()
	var roundID int64
	if err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO rounds (quiz_id, position, title) VALUES (?, 1, 'R') RETURNING id`, quizID,
	).Scan(&roundID); err != nil {
		t.Fatalf("seed round err = %v, want nil", err)
	}

	return roundID
}

func seedQuestion(t *testing.T, db *sql.DB, quizID, roundID int64, position int) int64 {
	t.Helper()
	var qID int64
	if err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO questions (quiz_id, round_id, text, position) VALUES (?, ?, 'Q', ?) RETURNING id`,
		quizID, roundID, position,
	).Scan(&qID); err != nil {
		t.Fatalf("seed question err = %v, want nil", err)
	}

	return qID
}

// seedCompletedGame inserts a games row plus one game_questions per supplied
// question id, so the finisher predicate (game_questions count >= questions
// count) treats the game as completed iff every quiz question is in the slice.
func seedCompletedGame(t *testing.T, db *sql.DB, gameID string, quizID int64, questionIDs []int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(
		ctx, `INSERT INTO games (id, quiz_id) VALUES (?, ?)`, gameID, quizID,
	); err != nil {
		t.Fatalf("seed game %q err = %v, want nil", gameID, err)
	}
	for _, qid := range questionIDs {
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
			 VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			gameID, qid,
		); err != nil {
			t.Fatalf("seed game_question for game %q err = %v, want nil", gameID, err)
		}
	}
}

func readPlayCount(t *testing.T, db *sql.DB, quizID int64) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(
		context.Background(), "SELECT play_count FROM quizzes WHERE id = ?", quizID,
	).Scan(&n); err != nil {
		t.Fatalf("read play_count err = %v, want nil", err)
	}

	return n
}
