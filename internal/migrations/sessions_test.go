package migrations_test

import (
	"context"
	"database/sql"
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

// sessionHostLastSeenVersion is the MP-10 slice-3 migration that adds the
// nullable host_last_seen_at column to sessions (a plain ADD COLUMN, no table
// rebuild).
const sessionHostLastSeenVersion = 20260608120000

// TestSessionHostLastSeenMigration_Column pins the MP-10 slice-3 schema
// (#687): host_last_seen_at exists on sessions, defaults to NULL, and accepts
// a timestamp; the Down drops it and the re-Up adds it back. dbtest.Open
// already ran every migration, so the live schema is what the Up produced.
func TestSessionHostLastSeenMigration_Column(t *testing.T) {
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
		 VALUES ('Live', 'live-host-seen-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code) VALUES ('sess-hs-1', ?, 1, 'HSN234')`,
		quizID,
	); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}

	// A fresh row has NULL host_last_seen_at, and the column accepts a write.
	var hostSeen sql.NullTime
	if err := db.QueryRowContext(
		ctx, "SELECT host_last_seen_at FROM sessions WHERE id = 'sess-hs-1'",
	).Scan(&hostSeen); err != nil {
		t.Fatalf("read host_last_seen_at err = %v, want nil", err)
	}
	if hostSeen.Valid {
		t.Errorf("host_last_seen_at on a fresh row = %v, want NULL", hostSeen.Time)
	}
	if _, err := db.ExecContext(
		ctx, "UPDATE sessions SET host_last_seen_at = CURRENT_TIMESTAMP WHERE id = 'sess-hs-1'",
	); err != nil {
		t.Errorf("set host_last_seen_at err = %v, want nil", err)
	}

	// Down drops the column; re-Up adds it back so a later write succeeds again.
	if err := goose.DownTo(db, ".", sessionHostLastSeenVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up after down err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx, "UPDATE sessions SET host_last_seen_at = CURRENT_TIMESTAMP WHERE id = 'sess-hs-1'",
	); err != nil {
		t.Errorf("set host_last_seen_at after re-up err = %v, want nil", err)
	}
}

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

// sessionPlayersDropDisplayNameVersion is the #716 migration that rebuilds
// session_players to drop the display_name column and its UNIQUE (session_id,
// display_name) constraint, keeping UNIQUE (session_id, player_id).
const sessionPlayersDropDisplayNameVersion = 20260609120000

// TestSessionsMigration_RosterUniqueness pins the surviving session_players
// UNIQUE constraint after #716 dropped the per-session display_name: one row
// per (session, player). display_name is no longer stored on the roster.
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
	var p1 int64
	if err := db.QueryRowContext(
		ctx, `INSERT INTO players (display_name, role) VALUES ('mig-join-1', 'player') RETURNING id`,
	).Scan(&p1); err != nil {
		t.Fatalf("seed player 1 err = %v, want nil", err)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO session_players (session_id, player_id) VALUES ('sess-roster-1', ?)`,
		p1,
	); err != nil {
		t.Fatalf("seed roster row err = %v, want nil", err)
	}

	t.Run("session_players has no display_name column", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx, `INSERT INTO session_players (session_id, player_id, display_name) VALUES ('sess-roster-1', 99, 'X')`,
		)
		if err == nil {
			t.Error("insert referencing display_name err = nil, want a no-such-column error")
		}
	})

	t.Run("same player twice in a session is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := db.ExecContext(
			ctx,
			`INSERT INTO session_players (session_id, player_id) VALUES ('sess-roster-1', ?)`,
			p1,
		)
		if err == nil {
			t.Error("duplicate player err = nil, want a UNIQUE violation")
		}
	})
}

// TestSessionPlayersDropDisplayNameMigration_RebuildPreservesRows pins the
// #716 child-table rebuild: a seeded roster row survives the Up with its id and
// columns intact, display_name is gone, the (session, player) UNIQUE remains,
// and the Down re-adds a nullable display_name so the round trip is clean.
func TestSessionPlayersDropDisplayNameMigration_RebuildPreservesRows(t *testing.T) {
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
		 VALUES ('Live', 'live-drop-name-mig', 'd', 1, 'live') RETURNING id`,
	).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, quiz_id, host_player_id, join_code) VALUES ('sess-dn-1', ?, 1, 'DNM234')`,
		quizID,
	); err != nil {
		t.Fatalf("seed session err = %v, want nil", err)
	}
	var playerID int64
	if err := db.QueryRowContext(
		ctx, `INSERT INTO players (display_name, role) VALUES ('drop-name-join', 'player') RETURNING id`,
	).Scan(&playerID); err != nil {
		t.Fatalf("seed player err = %v, want nil", err)
	}
	var rosterID int64
	if err := db.QueryRowContext(
		ctx,
		`INSERT INTO session_players (session_id, player_id, is_ready) VALUES ('sess-dn-1', ?, 1) RETURNING id`,
		playerID,
	).Scan(&rosterID); err != nil {
		t.Fatalf("seed roster row err = %v, want nil", err)
	}

	// Down re-adds display_name (nullable, default ''); the row survives.
	if err := goose.DownTo(db, ".", sessionPlayersDropDisplayNameVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	var (
		downID      int64
		downName    string
		downIsReady int64
	)
	if err := db.QueryRowContext(
		ctx, "SELECT id, display_name, is_ready FROM session_players WHERE session_id = 'sess-dn-1'",
	).Scan(&downID, &downName, &downIsReady); err != nil {
		t.Fatalf("read roster row after down err = %v, want nil", err)
	}
	if got, want := downID, rosterID; got != want {
		t.Errorf("roster id after down = %d, want %d (id preserved)", got, want)
	}
	if got, want := downName, ""; got != want {
		t.Errorf("display_name after down = %q, want %q (re-added empty)", got, want)
	}
	if got, want := downIsReady, int64(1); got != want {
		t.Errorf("is_ready after down = %d, want %d (preserved)", got, want)
	}

	// Re-Up drops display_name again and keeps the row + (session, player) UNIQUE.
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up after down err = %v, want nil", err)
	}
	var upID int64
	if err := db.QueryRowContext(
		ctx, "SELECT id FROM session_players WHERE session_id = 'sess-dn-1'",
	).Scan(&upID); err != nil {
		t.Fatalf("read roster row after re-up err = %v, want nil", err)
	}
	if got, want := upID, rosterID; got != want {
		t.Errorf("roster id after re-up = %d, want %d (id preserved across round trip)", got, want)
	}
	if _, err := db.ExecContext(
		ctx, `INSERT INTO session_players (session_id, player_id) VALUES ('sess-dn-1', ?)`, playerID,
	); err == nil {
		t.Error("duplicate (session, player) after re-up err = nil, want a UNIQUE violation")
	}
}
