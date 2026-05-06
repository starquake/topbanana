package store_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/store"
)

func TestPlayerStore_Ping(t *testing.T) {
	t.Parallel()

	t.Run("ping success", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		if err := ps.Ping(t.Context()); err != nil {
			t.Errorf("Ping() err = %v, want nil", err)
		}
	})

	t.Run("ping failure", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		if err := db.Close(); err != nil {
			t.Fatalf("failed to close database: %v", err)
		}

		err := ps.Ping(t.Context())
		if err == nil {
			t.Fatal("Ping() err = nil, want non-nil")
		}
		if got, want := err.Error(), "failed to ping database"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, want it to contain %q", got, want)
		}
	})
}

func TestPlayerStore_CreateAndGetPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreatePlayer(t.Context(), "alice", "hashed-secret", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Username, "alice"; got != want {
		t.Errorf("CreatePlayer Username = %q, want %q", got, want)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("CreatePlayer Role = %q, want %q", got, want)
	}
	if got, want := created.PasswordHash, "hashed-secret"; got != want {
		t.Errorf("CreatePlayer PasswordHash = %q, want %q", got, want)
	}

	fetched, err := ps.GetPlayerByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := fetched.ID, created.ID; got != want {
		t.Errorf("GetPlayerByUsername ID = %d, want %d", got, want)
	}
	if got, want := fetched.Role, auth.RoleAdmin; got != want {
		t.Errorf("GetPlayerByUsername Role = %q, want %q", got, want)
	}
}

func TestPlayerStore_CreatePlayer_FirstPasswordBearer_PromotedToAdmin(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// Even when the caller asks for "player", the first password-bearing
	// registrant is promoted to admin atomically by the SQL CASE expression.
	created, err := ps.CreatePlayer(t.Context(), "alice", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestPlayerStore_CreatePlayer_SecondPasswordBearer_StaysPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	created, err := ps.CreatePlayer(t.Context(), "bob", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RolePlayer; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestPlayerStore_CreatePlayer_ExplicitAdmin_HonouredEvenWhenNotFirst(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	created, err := ps.CreatePlayer(t.Context(), "carol", "hash", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestPlayerStore_TrimsWhitespaceOnCreateAndLookup(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "  alice  ", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	// Lookup with a trailing space matches because the store trims.
	fetched, err := ps.GetPlayerByUsername(t.Context(), "alice ")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := fetched.Username, "alice"; got != want {
		t.Errorf("Username = %q, want %q (whitespace should have been trimmed)", got, want)
	}
}

func TestPlayerStore_GetPlayerByUsername_NotFound(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	_, err := ps.GetPlayerByUsername(t.Context(), "ghost")
	if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_CreatePlayer_DuplicateUsername(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer first call err = %v, want nil", err)
	}

	_, err := ps.CreatePlayer(t.Context(), "alice", "other", auth.RolePlayer)
	if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}
