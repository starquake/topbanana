package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// uniqueGameQuestionVersion is the migration that adds the UNIQUE INDEX on
// game_questions(game_id, question_id) to prevent double-issuance.
const uniqueGameQuestionVersion = 20260624120000

// TestUniqueGameQuestionMigration_RejectsDuplicate pins that the unique index
// prevents two game_questions rows for the same (game_id, question_id) pair,
// which is the core invariant the migration enforces. A second insert with the
// same pair must fail with a constraint violation.
func TestUniqueGameQuestionMigration_RejectsDuplicate(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Unique GQ", "unique-gq-mig")
	roundID := seedRound(t, db, quizID)
	qID := seedQuestion(t, db, quizID, roundID, 1)

	ctx := t.Context()
	if _, err := db.ExecContext(
		ctx, `INSERT INTO games (id, quiz_id) VALUES ('gq-dup-game', ?)`, quizID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	insertGameQuestion := func() error {
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
			 VALUES ('gq-dup-game', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			qID,
		)

		return err
	}

	if err := insertGameQuestion(); err != nil {
		t.Fatalf("first insert err = %v, want nil", err)
	}

	if err := insertGameQuestion(); err == nil {
		t.Error("second insert err = nil, want a UNIQUE constraint violation")
	}
}

// TestUniqueGameQuestionMigration_DownDropsIndex pins that the Down migration
// removes the unique index so duplicates are again tolerated (the schema
// returns to its pre-migration shape).
func TestUniqueGameQuestionMigration_DownDropsIndex(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Unique GQ Down", "unique-gq-down-mig")
	roundID := seedRound(t, db, quizID)
	qID := seedQuestion(t, db, quizID, roundID, 1)

	ctx := t.Context()
	if _, err := db.ExecContext(
		ctx, `INSERT INTO games (id, quiz_id) VALUES ('gq-down-game', ?)`, quizID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	insertGameQuestion := func() error {
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
			 VALUES ('gq-down-game', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			qID,
		)

		return err
	}

	if err := insertGameQuestion(); err != nil {
		t.Fatalf("first insert err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", uniqueGameQuestionVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	if err := insertGameQuestion(); err != nil {
		t.Errorf("post-down duplicate insert err = %v, want nil (index dropped)", err)
	}

	// Remove the duplicate row so goose.Up can re-create the unique index
	// without a pre-existing conflict.
	if _, err := db.ExecContext(
		ctx,
		`DELETE FROM game_questions WHERE game_id = 'gq-down-game' AND question_id = ?`,
		qID,
	); err != nil {
		t.Fatalf("cleanup delete err = %v, want nil", err)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
}
