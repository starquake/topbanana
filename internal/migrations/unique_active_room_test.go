package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// uniqueActiveRoomVersion is the #1336 migration adding the one-active-room-per-
// host partial unique index on sessions.
const uniqueActiveRoomVersion = 20260925120000

// TestUniqueActiveRoomMigration_ClosesDuplicatesAndEnforces pins the #1336
// migration: a host's extra active rooms are finished (the newest stays open),
// other hosts' rooms are untouched, and afterwards a second active room is
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
	var otherHost int64
	if err := db.QueryRowContext(
		ctx, `INSERT INTO players (display_name, role) VALUES ('uar-other-host', 'host') RETURNING id`,
	).Scan(&otherHost); err != nil {
		t.Fatalf("seed other host err = %v, want nil", err)
	}
	seed := []struct {
		id, code, phase, createdAt string
		host                       int64
	}{
		{"uar-old", "UAR001", "lobby", "2026-06-01 10:00:00", 1},
		{"uar-tie-a", "UAR002", "question", "2026-06-02 10:00:00", 1},
		{"uar-tie-b", "UAR003", "intermission", "2026-06-02 10:00:00", 1},
		{"uar-done", "UAR004", "finished", "2026-06-03 10:00:00", 1},
		{"uar-other", "UAR005", "lobby", "2026-06-01 09:00:00", otherHost},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO sessions (id, host_player_id, join_code, phase, created_at) VALUES (?, ?, ?, ?, ?)`,
			s.id, s.host, s.code, s.phase, s.createdAt,
		); err != nil {
			t.Fatalf("seed session %s err = %v, want nil", s.id, err)
		}
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	// The newest active room wins; a created_at tie falls to the higher id.
	want := map[string]string{
		"uar-old":   "finished",
		"uar-tie-a": "finished",
		"uar-tie-b": "intermission",
		"uar-done":  "finished",
		"uar-other": "lobby",
	}
	for id, wantPhase := range want {
		var phase string
		var finished bool
		if err := db.QueryRowContext(
			ctx, "SELECT phase, finished_at IS NOT NULL FROM sessions WHERE id = ?", id,
		).Scan(&phase, &finished); err != nil {
			t.Fatalf("read session %s err = %v, want nil", id, err)
		}
		if got := phase; got != wantPhase {
			t.Errorf("session %s phase = %q, want %q", id, got, wantPhase)
		}
		if got, want := finished, wantPhase == "finished"; id != "uar-done" && got != want {
			t.Errorf("session %s has finished_at = %v, want %v", id, got, want)
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
