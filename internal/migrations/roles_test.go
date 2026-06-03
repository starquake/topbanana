package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// versionBeforeRolesRemap is the migration version immediately before the
// player/host/admin remap (20260529160000). The test migrates up to here,
// seeds rows in every old role state, then runs the remap so the mapping is
// pinned against real goose-applied schema rather than a hand-built table.
const versionBeforeRolesRemap = 20260529140000

// TestRolesMigration_RemapsTiers is the anti-mixing guard for #538: an old
// super admin must land on 'admin' (top), an old plain admin on 'host'
// (middle), a plain player on 'player', and the id=1 seed admin (role='admin',
// is_super_admin=0) on 'host' - it must NOT be promoted to the top tier.
func TestRolesMigration_RemapsTiers(t *testing.T) {
	t.Parallel()

	db := dbtest.OpenUnmigrated(t)

	if err := goose.UpTo(db, ".", versionBeforeRolesRemap); err != nil {
		t.Fatalf("migrate up to %d: %v", versionBeforeRolesRemap, err)
	}

	// id=1 is the seed admin (role='admin', is_super_admin=0) from
	// 20260111110308_add_admin_player.sql; it is already present.
	seed := []struct {
		displayName  string
		role         string
		isSuperAdmin int
	}{
		{displayName: "plain_player", role: "player", isSuperAdmin: 0},
		{displayName: "plain_admin", role: "admin", isSuperAdmin: 0},
		{displayName: "super_admin", role: "admin", isSuperAdmin: 1},
	}
	for _, s := range seed {
		// The column is still named username at versionBeforeRolesRemap; a
		// later migration that goose.Up applies below renames it to
		// display_name, which is why the assertion query reads display_name.
		if _, err := db.ExecContext(
			t.Context(),
			"INSERT INTO players (username, role, is_super_admin) VALUES (?, ?, ?)",
			s.displayName, s.role, s.isSuperAdmin,
		); err != nil {
			t.Fatalf("seed %q: %v", s.displayName, err)
		}
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("run remap migration: %v", err)
	}

	want := map[string]string{
		"plain_player": "player",
		"plain_admin":  "host",
		"super_admin":  "admin",
	}
	for displayName, wantRole := range want {
		var gotRole string
		if err := db.QueryRowContext(
			t.Context(), "SELECT role FROM players WHERE display_name = ?", displayName,
		).Scan(&gotRole); err != nil {
			t.Fatalf("read role for %q: %v", displayName, err)
		}
		if gotRole != wantRole {
			t.Errorf("role for %q = %q, want %q", displayName, gotRole, wantRole)
		}
	}

	// The seed admin (id=1) must become host, not admin: it was plain admin
	// pre-migration, so the top tier would be a silent over-promotion.
	var seedRole string
	if err := db.QueryRowContext(
		t.Context(), "SELECT role FROM players WHERE id = 1",
	).Scan(&seedRole); err != nil {
		t.Fatalf("read seed admin role: %v", err)
	}
	if got, want := seedRole, "host"; got != want {
		t.Errorf("seed admin (id=1) role = %q, want %q", got, want)
	}

	// is_super_admin must be gone after the rebuild.
	if _, err := db.ExecContext(t.Context(), "SELECT is_super_admin FROM players"); err == nil {
		t.Error("is_super_admin column still present, want it dropped")
	}
}
