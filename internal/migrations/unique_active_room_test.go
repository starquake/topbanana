package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// uniqueActiveRoomVersion is the #1336 migration adding the one-active-room-per-
// host partial unique index on sessions.
const uniqueActiveRoomVersion = 20260925150000

// TestUniqueActiveRoomMigration_ClosesDuplicatesAndEnforces pins the #1336
// migration: a host's extra active rooms are finished (a running game, then the
// latest host heartbeat, then the newest room stays open), other hosts' rooms
// are untouched, and afterwards a second active room is
// rejected while a finished one is still allowed. The Down drops the index.
func TestUniqueActiveRoomMigration_ClosesDuplicatesAndEnforces(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if err := goose.DownTo(db, ".", uniqueActiveRoomVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	hostID := func(name string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRowContext(
			ctx, `INSERT INTO players (display_name, role) VALUES (?, 'host') RETURNING id`, name,
		).Scan(&id); err != nil {
			t.Fatalf("seed host %q err = %v, want nil", name, err)
		}

		return id
	}
	beatHost, tieHost, otherHost := hostID("uar-beat-host"), hostID("uar-tie-host"), hostID("uar-other-host")
	seed := []struct {
		id, code, phase, createdAt string
		hostLastSeenAt             any
		host                       int64
	}{
		{"uar-running", "UAR001", "question", "2026-06-01 10:00:00", "2026-06-01 10:05:00", 1},
		{"uar-dup-lobby", "UAR002", "lobby", "2026-06-01 10:00:05", "2026-06-01 10:05:30", 1},
		{"uar-done", "UAR004", "finished", "2026-06-03 10:00:00", nil, 1},
		{"uar-beat-old", "UAR009", "lobby", "2026-06-01 10:00:00", "2026-06-02 10:00:00", beatHost},
		{"uar-beat-new", "UAR010", "intermission", "2026-06-01 11:00:00", "2026-06-01 11:00:00", beatHost},
		{"uar-beat-none", "UAR011", "lobby", "2026-06-01 12:00:00", nil, beatHost},
		{"uar-tie-a", "UAR012", "lobby", "2026-06-02 10:00:00", nil, tieHost},
		{"uar-tie-b", "UAR013", "intermission", "2026-06-02 10:00:00", nil, tieHost},
		{"uar-other", "UAR005", "lobby", "2026-06-01 09:00:00", nil, otherHost},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO sessions (id, host_player_id, join_code, phase, created_at, host_last_seen_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.host, s.code, s.phase, s.createdAt, s.hostLastSeenAt,
		); err != nil {
			t.Fatalf("seed session %q err = %v, want nil", s.id, err)
		}
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	// A game in flight beats a newer lobby, then the latest host heartbeat
	// wins, then the newest room, and a created_at tie falls to the higher id.
	wantPhases := map[string]string{
		"uar-running":   "question",
		"uar-dup-lobby": "finished",
		"uar-done":      "finished",
		"uar-beat-old":  "lobby",
		"uar-beat-new":  "finished",
		"uar-beat-none": "finished",
		"uar-tie-a":     "finished",
		"uar-tie-b":     "intermission",
		"uar-other":     "lobby",
	}
	for id, wantPhase := range wantPhases {
		var phase string
		var finished bool
		if err := db.QueryRowContext(
			ctx, "SELECT phase, finished_at IS NOT NULL FROM sessions WHERE id = ?", id,
		).Scan(&phase, &finished); err != nil {
			t.Fatalf("read session %q err = %v, want nil", id, err)
		}
		if phase != wantPhase {
			t.Errorf("session %q phase = %q, want %q", id, phase, wantPhase)
		}
		// uar-done was seeded finished without a finished_at.
		if got, want := finished, wantPhase == "finished"; id != "uar-done" && got != want {
			t.Errorf("session %q has finished_at = %v, want %v", id, got, want)
		}
	}

	if _, err := db.ExecContext(
		ctx, `INSERT INTO sessions (id, host_player_id, join_code) VALUES ('uar-extra', 1, 'UAR006')`,
	); err == nil {
		t.Error("insert second active room err = nil, want a UNIQUE violation")
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions (id, host_player_id, join_code, phase) VALUES ('uar-closed', 1, 'UAR007', 'finished')`,
	); err != nil {
		t.Errorf("insert finished room err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", uniqueActiveRoomVersion-1); err != nil {
		t.Fatalf("goose.DownTo after up err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		ctx, `INSERT INTO sessions (id, host_player_id, join_code) VALUES ('uar-after-down', 1, 'UAR008')`,
	); err != nil {
		t.Errorf("insert second active room after down err = %v, want nil", err)
	}
}
