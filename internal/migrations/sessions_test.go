package migrations_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// sessionRunnerVersion is the session-runner migration that rebuilds the
// sessions parent table to widen the phase CHECK and add the runner columns.
const sessionRunnerVersion = 20260606120000

// sessionRoundResultsVersion is the MP-6 migration that rebuilds the sessions
// parent table to add the round_results phase to the phase CHECK.
const sessionRoundResultsVersion = 20260607120000

// TestSessionRoundResultsMigration_PhaseCheck pins the MP-6 schema (#683): the
// widened phase CHECK accepts round_results, and the Down rebuild coerces a
// session sitting in round_results back to reveal to satisfy the old narrower
// CHECK, then the re-Up accepts round_results again. dbtest.Open already ran
// every migration, so the live schema is what the Up produced.
func TestSessionRoundResultsMigration_PhaseCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	var quizID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO quizzes (title, slug, description, created_by_player_id, mode)
		 VALUES ('Live', 'live-round-results-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code, phase)
		 VALUES ('sess-rr-1', ?, 1, 'RRS234', 'round_results')`,
		quizID,
	); err != nil {
		t.Fatalf("seed round_results session err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", sessionRoundResultsVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	// After the rollback the session survives with round_results coerced to
	// reveal.
	var phase string
	if err := db.QueryRowContext(ctx, "SELECT phase FROM sessions WHERE id = 'sess-rr-1'").Scan(&phase); err != nil {
		t.Fatalf("read phase after down err = %v, want nil", err)
	}
	if got, want := phase, "reveal"; got != want {
		t.Errorf("phase after down = %q, want %q", got, want)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up after down err = %v, want nil", err)
	}

	// The widened CHECK is back: round_results is accepted again.
	if _, err := db.ExecContext(
		ctx, "UPDATE sessions SET phase = 'round_results' WHERE id = 'sess-rr-1'",
	); err != nil {
		t.Errorf("update to round_results after re-up err = %v, want nil", err)
	}
}

// TestSessionRunnerMigration_DownUpRoundTrip exercises the parent-table
// rebuild's Down path (the risky one: dropping a parent with foreign_keys=OFF
// inside an explicit transaction) and the re-Up afterwards, with a seeded
// session row, so a broken rollback fails loudly here rather than in
// production. The Down coerces any past-lobby phase back to 'lobby' to satisfy
// the old narrow CHECK; this asserts the row survives the round trip.
func TestSessionRunnerMigration_DownUpRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	var quizID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO quizzes (title, slug, description, created_by_player_id, mode)
		 VALUES ('Live', 'live-roundtrip-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code, phase)
		 VALUES ('sess-rt-1', ?, 1, 'RTP234', 'question')`,
		quizID,
	); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", sessionRunnerVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	// After the rollback the session survives with phase coerced to 'lobby'.
	var phase string
	if err := db.QueryRowContext(ctx, "SELECT phase FROM sessions WHERE id = 'sess-rt-1'").Scan(&phase); err != nil {
		t.Fatalf("read phase after down err = %v, want nil", err)
	}
	if got, want := phase, "lobby"; got != want {
		t.Errorf("phase after down = %q, want %q", got, want)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up after down err = %v, want nil", err)
	}

	// The widened CHECK is back: a runner phase is accepted again.
	if _, err := db.ExecContext(
		ctx, "UPDATE sessions SET phase = 'reveal' WHERE id = 'sess-rt-1'",
	); err != nil {
		t.Errorf("update to runner phase after re-up err = %v, want nil", err)
	}
}

// TestSessionsMigration_Constraints pins the MP-1 schema (#678): the
// sessions and session_players tables exist with their UNIQUE and CHECK
// constraints. dbtest.Open already ran every migration, so the live schema
// is what the migration produced.
func TestSessionsMigration_Constraints(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	// Seed a quiz + host player so the FK columns are satisfiable. The
	// seeded admin (id=1) from the auth migration backs host_player_id.
	var quizID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO quizzes (title, slug, description, created_by_player_id, mode)
		 VALUES ('Live', 'live-sessions-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}

	const code = "ABC234"
	var sessionID string
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code)
		 VALUES ('sess-mig-1', ?, 1, ?) RETURNING id`,
		quizID, code,
	).Scan(&sessionID); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}

	t.Run("phase defaults to lobby", func(t *testing.T) {
		t.Parallel()
		var phase string
		if err := db.QueryRowContext(
			ctx, "SELECT phase FROM sessions WHERE id = ?", sessionID,
		).Scan(&phase); err != nil {
			t.Fatalf("read phase err = %v, want nil", err)
		}
		if got, want := phase, "lobby"; got != want {
			t.Errorf("phase = %q, want %q", got, want)
		}
	})

	t.Run("join_code is unique", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO sessions (id, quiz_id, host_player_id, join_code) VALUES ('sess-mig-dup', ?, 1, ?)`,
			quizID, code,
		)
		if err == nil {
			t.Error("duplicate join_code insert err = nil, want a UNIQUE violation")
		}
	})

	t.Run("phase CHECK rejects an unknown phase", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO sessions (id, quiz_id, host_player_id, join_code, phase)
			 VALUES ('sess-mig-bad', ?, 1, 'PHZ234', 'nonsense')`,
			quizID,
		)
		if err == nil {
			t.Error("insert with unknown phase err = nil, want a CHECK violation")
		}
	})
}

// TestSessionRunnerMigration_PhasesAndAnswers pins the MP-5 schema (#682):
// the widened phase CHECK accepts the runner phases, the runner timing
// columns exist on sessions, and session_answers enforces one pick per
// (session, question, player). dbtest.Open ran every migration including the
// table rebuild, so this asserts the rebuild preserved the lobby row and
// produced the new shape.
func TestSessionRunnerMigration_PhasesAndAnswers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	var quizID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO quizzes (title, slug, description, created_by_player_id, mode)
		 VALUES ('Live', 'live-runner-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code) VALUES ('sess-run-1', ?, 1, 'RUN234')`,
		quizID,
	); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}

	t.Run("phase CHECK accepts the runner phases", func(t *testing.T) {
		t.Parallel()
		for _, phase := range []string{"round_intro", "question", "reveal", "finished"} {
			if _, err := db.ExecContext(
				ctx, "UPDATE sessions SET phase = ? WHERE id = 'sess-run-1'", phase,
			); err != nil {
				t.Errorf("update phase to %q err = %v, want nil", phase, err)
			}
		}
	})

	t.Run("session_answers is unique per (session, question, player)", func(t *testing.T) {
		t.Parallel()
		var roundID int64
		if err := db.QueryRowContext(
			ctx, `INSERT INTO rounds (quiz_id, position, title) VALUES (?, 1, 'R') RETURNING id`, quizID,
		).Scan(&roundID); err != nil {
			t.Fatalf("seed round err = %v, want nil", err)
		}
		var questionID int64
		if err := db.QueryRowContext(
			ctx,
			`INSERT INTO questions (quiz_id, round_id, text, position) VALUES (?, ?, 'Q', 1) RETURNING id`,
			quizID, roundID,
		).Scan(&questionID); err != nil {
			t.Fatalf("seed question err = %v, want nil", err)
		}
		var optionID int64
		if err := db.QueryRowContext(
			ctx,
			`INSERT INTO options (question_id, text, is_correct) VALUES (?, 'A', 1) RETURNING id`,
			questionID,
		).Scan(&optionID); err != nil {
			t.Fatalf("seed option err = %v, want nil", err)
		}
		var playerID int64
		if err := db.QueryRowContext(
			ctx, `INSERT INTO players (display_name, role) VALUES ('run-join-1', 'player') RETURNING id`,
		).Scan(&playerID); err != nil {
			t.Fatalf("seed player err = %v, want nil", err)
		}
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO session_answers (session_id, question_id, player_id, option_id)
			 VALUES ('sess-run-1', ?, ?, ?)`,
			questionID, playerID, optionID,
		); err != nil {
			t.Fatalf("seed answer err = %v, want nil", err)
		}

		_, err := db.ExecContext(
			ctx,
			`INSERT INTO session_answers (session_id, question_id, player_id, option_id)
			 VALUES ('sess-run-1', ?, ?, ?)`,
			questionID, playerID, optionID,
		)
		if err == nil {
			t.Error("duplicate answer err = nil, want a UNIQUE violation")
		}
	})
}

// TestSessionsMigration_RosterUniqueness pins the session_players UNIQUE
// constraints: one row per (session, player) and per (session,
// display_name).
func TestSessionsMigration_RosterUniqueness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	var quizID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO quizzes (title, slug, description, created_by_player_id, mode)
		 VALUES ('Live', 'live-roster-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code) VALUES ('sess-roster-1', ?, 1, 'RST234')`,
		quizID,
	); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}
	// Two distinct joining players.
	var p1, p2 int64
	if err := db.QueryRowContext(
		ctx, `INSERT INTO players (display_name, role) VALUES ('mig-join-1', 'player') RETURNING id`,
	).Scan(&p1); err != nil {
		t.Fatalf("seed player 1 err = %v, want nil", err)
	}
	if err := db.QueryRowContext(
		ctx, `INSERT INTO players (display_name, role) VALUES ('mig-join-2', 'player') RETURNING id`,
	).Scan(&p2); err != nil {
		t.Fatalf("seed player 2 err = %v, want nil", err)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO session_players (session_id, player_id, display_name) VALUES ('sess-roster-1', ?, 'Alice')`,
		p1,
	); err != nil {
		t.Fatalf("seed roster row err = %v, want nil", err)
	}

	t.Run("same display_name in a session is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO session_players (session_id, player_id, display_name) VALUES ('sess-roster-1', ?, 'Alice')`,
			p2,
		)
		if err == nil {
			t.Error("duplicate display_name err = nil, want a UNIQUE violation")
		}
	})

	t.Run("same player twice in a session is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO session_players (session_id, player_id, display_name) VALUES ('sess-roster-1', ?, 'Alice2')`,
			p1,
		)
		if err == nil {
			t.Error("duplicate player err = nil, want a UNIQUE violation")
		}
	})
}
