package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// playerApprovedAtVersion is the #1227 ADD COLUMN migration adding
// players.approved_at.
const playerApprovedAtVersion = 20260710120000

// TestPlayerApprovedAtMigration_Column pins the #1227 schema addition: players
// gains an approved_at column.
func TestPlayerApprovedAtMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "players")["approved_at"] {
		t.Error("players is missing the approved_at column")
	}
}

// TestPlayerApprovedAtMigration_BackfillsExistingRows pins the backfill: a row
// that existed before the migration is stamped approved so turning
// LOGIN_APPROVAL_REQUIRED on later never locks it out.
func TestPlayerApprovedAtMigration_BackfillsExistingRows(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	// Roll back to just before the migration so the column is absent, then seed
	// a pre-migration row.
	if err := goose.DownTo(db, ".", playerApprovedAtVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if _, err := db.ExecContext(
		t.Context(), "INSERT INTO players (display_name, role) VALUES ('legacy-player', 'player')",
	); err != nil {
		t.Fatalf("seed legacy player err = %v, want nil", err)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	var approved bool
	if err := db.QueryRowContext(
		t.Context(), "SELECT approved_at IS NOT NULL FROM players WHERE display_name = 'legacy-player'",
	).Scan(&approved); err != nil {
		t.Fatalf("read approved_at err = %v, want nil", err)
	}
	if !approved {
		t.Error("legacy player approved_at IS NULL after migration, want it backfilled")
	}
}

// TestPlayerApprovedAtMigration_FreshRowDefaultsUnapproved pins that a row
// inserted after the migration has no approved_at (there is no column DEFAULT),
// and that stamping it round-trips.
func TestPlayerApprovedAtMigration_FreshRowDefaultsUnapproved(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if _, err := db.ExecContext(
		t.Context(), "INSERT INTO players (display_name, role) VALUES ('fresh-player', 'player')",
	); err != nil {
		t.Fatalf("seed fresh player err = %v, want nil", err)
	}

	var approved bool
	if err := db.QueryRowContext(
		t.Context(), "SELECT approved_at IS NOT NULL FROM players WHERE display_name = 'fresh-player'",
	).Scan(&approved); err != nil {
		t.Fatalf("read approved_at err = %v, want nil", err)
	}
	if approved {
		t.Error("fresh player approved_at IS NOT NULL, want NULL (no column default)")
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE players SET approved_at = CURRENT_TIMESTAMP WHERE display_name = 'fresh-player'",
	); err != nil {
		t.Fatalf("stamp approved_at err = %v, want nil", err)
	}
	if err := db.QueryRowContext(
		t.Context(), "SELECT approved_at IS NOT NULL FROM players WHERE display_name = 'fresh-player'",
	).Scan(&approved); err != nil {
		t.Fatalf("re-read approved_at err = %v, want nil", err)
	}
	if !approved {
		t.Error("fresh player approved_at IS NULL after stamp, want set")
	}
}
