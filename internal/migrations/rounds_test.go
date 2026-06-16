package migrations_test

import (
	"database/sql"
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestRoundsMigration_BackfillsDefaultRound asserts the rounds backfill
// (#444): a quiz lands with a default 'Round 1' and every question
// resolves to it via the questions.round_id FK the migration added.
// dbtest.Open already ran every migration including the backfill, so a
// quiz created through the store is indistinguishable from one migrated
// by it.
func TestRoundsMigration_BackfillsDefaultRound(t *testing.T) {
	t.Parallel()

	const wantTitle = "Round 1"

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizStore := store.NewQuizStore(db, slog.Default())

	creatorID := seedPlayer(t, db)
	qz := &quiz.Quiz{
		Title:             "Migrated quiz",
		Slug:              "migrated-quiz",
		Description:       "seeded pre-rounds",
		CreatedByPlayerID: creatorID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	round, err := quizStore.GetDefaultRound(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	if got := round.Title; got != wantTitle {
		t.Errorf("default round title = %q, want %q", got, wantTitle)
	}
	if got, want := round.QuizID, qz.ID; got != want {
		t.Errorf("default round quiz_id = %d, want %d", got, want)
	}

	// The seeded question must resolve to the default round via the
	// questions.round_id FK the migration added.
	gotRoundID := questionRoundID(t, db, qz.Questions[0].ID)
	if got, want := gotRoundID, round.ID; got != want {
		t.Errorf("question round_id = %d, want %d", got, want)
	}
}

// TestRoundSeenPhaseMigration_PerPhasePK asserts the #548 rebuild of
// game_seen_rounds: the composite PK (game_id, round_id, phase) lets the
// intro and results phases of the same round coexist, while the CHECK
// constraint rejects any phase outside ('intro','results'). dbtest.Open
// already ran every migration including the rebuild, so the live schema
// is what the migration produced.
func TestRoundSeenPhaseMigration_PerPhasePK(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizStore := store.NewQuizStore(db, slog.Default())
	creatorID := seedPlayer(t, db)
	qz := &quiz.Quiz{
		Title:             "Phase quiz",
		Slug:              "phase-quiz",
		CreatedByPlayerID: creatorID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizStore.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	round, err := quizStore.GetDefaultRound(ctx, qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}

	const gameID = "game-phase-1"
	if _, err = db.ExecContext(
		ctx,
		"INSERT INTO games (id, quiz_id) VALUES (?, ?)",
		gameID, qz.ID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	// Both phases of the same round must coexist under the composite PK.
	for _, phase := range []string{"intro", "results"} {
		if _, err = db.ExecContext(
			ctx,
			"INSERT INTO game_seen_rounds (game_id, round_id, phase) VALUES (?, ?, ?)",
			gameID, round.ID, phase,
		); err != nil {
			t.Fatalf("insert phase %q err = %v, want nil", phase, err)
		}
	}

	var count int
	if err = db.QueryRowContext(
		ctx,
		"SELECT count(*) FROM game_seen_rounds WHERE game_id = ? AND round_id = ?",
		gameID, round.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count seen rows err = %v, want nil", err)
	}
	if got, want := count, 2; got != want {
		t.Errorf("seen rows = %d, want %d (one per phase)", got, want)
	}

	// The CHECK constraint must reject an unknown phase.
	if _, err = db.ExecContext(
		ctx,
		"INSERT INTO game_seen_rounds (game_id, round_id, phase) VALUES (?, ?, 'bogus')",
		gameID, round.ID,
	); err == nil {
		t.Error("insert with unknown phase err = nil, want a CHECK violation")
	}
}

// TestRoundBoundaryDurationMigration_NullableWithCheck asserts the #554
// ADD COLUMN: rounds.boundary_duration_seconds defaults to NULL (inherit
// the quiz default), accepts an in-range value, and the CHECK constraint
// rejects an out-of-range one. dbtest.Open already ran the migration, so
// the live schema is what it produced.
func TestRoundBoundaryDurationMigration_NullableWithCheck(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizStore := store.NewQuizStore(db, slog.Default())
	creatorID := seedPlayer(t, db)
	qz := &quiz.Quiz{
		Title:             "Boundary quiz",
		Slug:              "boundary-quiz",
		CreatedByPlayerID: creatorID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizStore.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	round, err := quizStore.GetDefaultRound(ctx, qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}

	// The default round lands with a NULL override.
	var dur sql.NullInt64
	if err = db.QueryRowContext(
		ctx, "SELECT boundary_duration_seconds FROM rounds WHERE id = ?", round.ID,
	).Scan(&dur); err != nil {
		t.Fatalf("scan boundary_duration_seconds err = %v, want nil", err)
	}
	if dur.Valid {
		t.Errorf("default round boundary_duration_seconds = %d, want NULL", dur.Int64)
	}

	// An in-range value is accepted.
	if _, err = db.ExecContext(
		ctx, "UPDATE rounds SET boundary_duration_seconds = 30 WHERE id = ?", round.ID,
	); err != nil {
		t.Fatalf("UPDATE in-range err = %v, want nil", err)
	}

	// An out-of-range value trips the CHECK constraint.
	if _, err = db.ExecContext(
		ctx, "UPDATE rounds SET boundary_duration_seconds = 9999 WHERE id = ?", round.ID,
	); err == nil {
		t.Error("UPDATE out-of-range err = nil, want a CHECK violation")
	}
}

// questionRoundID reads the round_id column for a question straight from
// the DB so the test pins the migration's FK wiring without routing
// through the store mapper.
func questionRoundID(t *testing.T, db *sql.DB, questionID int64) int64 {
	t.Helper()
	var roundID int64
	if err := db.QueryRowContext(
		t.Context(),
		"SELECT round_id FROM questions WHERE id = ?",
		questionID,
	).Scan(&roundID); err != nil {
		t.Fatalf("scan round_id err = %v, want nil", err)
	}

	return roundID
}

// seedPlayer inserts a minimal player row so a quiz has a valid
// created_by_player_id, returning its id.
func seedPlayer(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(
		t.Context(),
		"INSERT INTO players (display_name, role) VALUES ('mig-admin', 'host')",
	)
	if err != nil {
		t.Fatalf("seed player err = %v, want nil", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId err = %v, want nil", err)
	}

	return id
}
