package migrations_test

import (
	"context"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
)

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
			 VALUES ('sess-mig-bad', ?, 1, 'PHZ234', 'question')`,
			quizID,
		)
		if err == nil {
			t.Error("insert with unknown phase err = nil, want a CHECK violation")
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
