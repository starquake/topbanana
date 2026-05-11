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
	// A registered user explicitly picked their username at the form, so
	// the frontend must see hasCustomName=true and skip the claim modal.
	if got, want := created.HasCustomName(), true; got != want {
		t.Errorf("CreatePlayer HasCustomName() = %v, want %v", got, want)
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
	if got, want := fetched.HasCustomName(), true; got != want {
		t.Errorf("GetPlayerByUsername HasCustomName() = %v, want %v (re-fetch must persist the flag)", got, want)
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

func TestPlayerStore_CreateAnonymousPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreateAnonymousPlayer(t.Context(), "anon-foo")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if got, want := created.Username, "anon-foo"; got != want {
		t.Errorf("Username = %q, want %q", got, want)
	}
	if got, want := created.PasswordHash, ""; got != want {
		t.Errorf("PasswordHash = %q, want %q (anonymous row should have NULL hash)", got, want)
	}
	if got, want := created.Role, auth.RolePlayer; got != want {
		t.Errorf("Role = %q, want %q (anonymous row never auto-promotes to admin)", got, want)
	}
	if !created.IsAnonymous() {
		t.Error("IsAnonymous() = false, want true")
	}
	// Fresh anonymous rows wear an auto-generated petname, not a name the
	// visitor picked, so the claim affordances must still render until they
	// rename via PATCH /api/players/me.
	if got, want := created.HasCustomName(), false; got != want {
		t.Errorf("HasCustomName() = %v, want %v (auto-petname is not a chosen name)", got, want)
	}
}

func TestPlayerStore_CreateAnonymousPlayer_DuplicateUsername(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreateAnonymousPlayer(t.Context(), "anon-clash"); err != nil {
		t.Fatalf("first CreateAnonymousPlayer err = %v, want nil", err)
	}

	_, err := ps.CreateAnonymousPlayer(t.Context(), "anon-clash")
	if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v (UNIQUE violation should map to ErrUsernameTaken)", got, want)
	}
}

func TestPlayerStore_CreateAnonymousPlayer_DoesNotBlockFirstAdmin(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// Seed an anonymous row first; the next CreatePlayer call should still
	// trigger the "first password-bearing registrant becomes admin" rule
	// because the SQL CASE filters by password_hash IS NOT NULL.
	if _, err := ps.CreateAnonymousPlayer(t.Context(), "anon-first"); err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	created, err := ps.CreatePlayer(t.Context(), "alice", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q (first credentialled player should still become admin)", got, want)
	}
}

func TestPlayerStore_ClaimPlayer_UpgradesAnonymousRow(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-claim")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	claimed, err := ps.ClaimPlayer(t.Context(), anon.ID, "alice", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("ClaimPlayer err = %v, want nil", err)
	}
	if got, want := claimed.ID, anon.ID; got != want {
		t.Errorf("claimed.ID = %d, want %d (claim must preserve player ID)", got, want)
	}
	if got, want := claimed.Username, "alice"; got != want {
		t.Errorf("claimed.Username = %q, want %q", got, want)
	}
	if got, want := claimed.PasswordHash, "hash"; got != want {
		t.Errorf("claimed.PasswordHash = %q, want %q", got, want)
	}
	// First password-bearing registrant — even via the claim path — becomes admin.
	if got, want := claimed.Role, auth.RoleAdmin; got != want {
		t.Errorf("claimed.Role = %q, want %q", got, want)
	}
	// The claim flow is an explicit username choice: the player typed it
	// into the register form, so the flag must flip alongside the password.
	if got, want := claimed.HasCustomName(), true; got != want {
		t.Errorf("claimed.HasCustomName() = %v, want %v", got, want)
	}
}

func TestPlayerStore_ClaimPlayer_AlreadyClaimed_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-twice")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, claimErr := ps.ClaimPlayer(t.Context(), anon.ID, "alice", "hash", auth.RolePlayer); claimErr != nil {
		t.Fatalf("first ClaimPlayer err = %v, want nil", claimErr)
	}

	_, err = ps.ClaimPlayer(t.Context(), anon.ID, "bob", "other", auth.RolePlayer)
	if got, want := err, auth.ErrPlayerAlreadyClaimed; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}

	// Make sure the original claim was not clobbered by the second attempt.
	stored, err := ps.GetPlayerByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := stored.ID, anon.ID; got != want {
		t.Errorf("stored.ID = %d, want %d (first claim should win)", got, want)
	}
}

func TestPlayerStore_ClaimPlayer_UnknownPlayerID_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	_, err := ps.ClaimPlayer(t.Context(), 9999, "ghost", "hash", auth.RolePlayer)
	if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_ClaimPlayer_UsernameTaken(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "h", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}
	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-rival")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	_, err = ps.ClaimPlayer(t.Context(), anon.ID, "alice", "h", auth.RolePlayer)
	if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_UpdatePlayerUsername(t *testing.T) {
	t.Parallel()

	t.Run("renames an anonymous player in place", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-xyz")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}
		// Sanity-check the precondition: a fresh anonymous row has not
		// claimed its username yet — that is what makes this scenario
		// meaningful.
		if got, want := anon.HasCustomName(), false; got != want {
			t.Fatalf("precondition anon.HasCustomName() = %v, want %v", got, want)
		}

		updated, err := ps.UpdatePlayerUsername(t.Context(), anon.ID, "alice")
		if err != nil {
			t.Fatalf("UpdatePlayerUsername err = %v, want nil", err)
		}
		if got, want := updated.ID, anon.ID; got != want {
			t.Errorf("updated.ID = %d, want %d (same row)", got, want)
		}
		if got, want := updated.Username, "alice"; got != want {
			t.Errorf("updated.Username = %q, want %q", got, want)
		}
		if got, want := updated.IsAnonymous(), true; got != want {
			t.Errorf("updated.IsAnonymous() = %v, want %v (no password set)", got, want)
		}
		// The frontend gates the end-of-quiz claim modal on hasCustomName,
		// so a successful PATCH must flip the flag; otherwise the modal
		// re-opens on the next finished quiz.
		if got, want := updated.HasCustomName(), true; got != want {
			t.Errorf("updated.HasCustomName() = %v, want %v (PATCH must mark the name as claimed)", got, want)
		}

		// Re-fetch by id to make sure the flag was persisted to the row,
		// not just returned by the RETURNING clause.
		refetched, err := ps.GetPlayerByID(t.Context(), anon.ID)
		if err != nil {
			t.Fatalf("GetPlayerByID err = %v, want nil", err)
		}
		if got, want := refetched.HasCustomName(), true; got != want {
			t.Errorf("refetched.HasCustomName() = %v, want %v (flag must persist across fetches)", got, want)
		}
	})

	t.Run("trims whitespace before storage", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-trim")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		updated, err := ps.UpdatePlayerUsername(t.Context(), anon.ID, "  bob  ")
		if err != nil {
			t.Fatalf("UpdatePlayerUsername err = %v, want nil", err)
		}
		if got, want := updated.Username, "bob"; got != want {
			t.Errorf("updated.Username = %q, want %q (whitespace trimmed)", got, want)
		}
	})

	t.Run("empty username returns ErrUsernameEmpty", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-empty")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		_, err = ps.UpdatePlayerUsername(t.Context(), anon.ID, "   ")
		if got, want := err, auth.ErrUsernameEmpty; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("collision returns ErrUsernameTaken", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		if _, err := ps.CreatePlayer(t.Context(), "claimed", "h", auth.RolePlayer); err != nil {
			t.Fatalf("seed CreatePlayer err = %v, want nil", err)
		}
		anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-collider")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		_, err = ps.UpdatePlayerUsername(t.Context(), anon.ID, "claimed")
		if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("non-anonymous row returns ErrPlayerNotAnonymous", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		credentialled, err := ps.CreatePlayer(t.Context(), "credentialled", "h", auth.RolePlayer)
		if err != nil {
			t.Fatalf("CreatePlayer err = %v, want nil", err)
		}

		_, err = ps.UpdatePlayerUsername(t.Context(), credentialled.ID, "newname")
		if got, want := err, auth.ErrPlayerNotAnonymous; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("unknown player ID returns ErrPlayerNotFound", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		_, err := ps.UpdatePlayerUsername(t.Context(), 99999, "ghost")
		if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}
