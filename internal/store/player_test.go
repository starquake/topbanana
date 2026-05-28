package store_test

import (
	"errors"
	"fmt"
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

	created, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hashed-secret", auth.RoleAdmin)
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
	created, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer)
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

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	created, err := ps.CreatePlayer(t.Context(), "bob", "bob"+"@example.test", "hash", auth.RolePlayer)
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

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	created, err := ps.CreatePlayer(t.Context(), "carol", "carol"+"@example.test", "hash", auth.RoleAdmin)
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

	if _, err := ps.CreatePlayer(
		t.Context(),
		"  alice  ",
		"  alice  "+"@example.test",
		"hash",
		auth.RolePlayer,
	); err != nil {
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

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer first call err = %v, want nil", err)
	}

	// Different email, same username -> ErrUsernameTaken.
	_, err := ps.CreatePlayer(t.Context(), "alice", "alice-other@example.test", "other", auth.RolePlayer)
	if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_CreatePlayer_DuplicateEmail(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "shared@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer first call err = %v, want nil", err)
	}

	_, err := ps.CreatePlayer(t.Context(), "bob", "shared@example.test", "other", auth.RolePlayer)
	if got, want := err, auth.ErrEmailTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_CreatePlayer_LowercasesAndTrimsEmail(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreatePlayer(t.Context(), "alice", "  ALICE@Example.Test ", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Email, "alice@example.test"; got != want {
		t.Errorf("stored Email = %q, want %q", got, want)
	}

	// Case-variant must still collide on the unique index.
	_, dupErr := ps.CreatePlayer(t.Context(), "bob", "alice@EXAMPLE.test", "h", auth.RolePlayer)
	if got, want := dupErr, auth.ErrEmailTaken; !errors.Is(got, want) {
		t.Errorf("case-variant duplicate err = %v, want %v", got, want)
	}
}

// TestPlayerStore_GetPlayerByEmail_LowercasesAndTrims pins the
// normalisation rule on the read path: CreatePlayer / ClaimPlayer /
// CreatePlayerFromOAuth all store the email lowercased and trimmed, so
// the lookup must apply the same transform or a mixed-case OIDC email
// would miss the link-by-email path and produce a duplicate row. See
// #471.
func TestPlayerStore_GetPlayerByEmail_LowercasesAndTrims(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreatePlayer(t.Context(), "alice", "alice@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	cases := []struct {
		name  string
		input string
	}{
		{"uppercase local part", "ALICE@example.test"},
		{"uppercase host", "alice@EXAMPLE.TEST"},
		{"mixed case", "Alice@Example.Test"},
		{"surrounding whitespace", "  alice@example.test  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ps.GetPlayerByEmail(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("GetPlayerByEmail(%q) err = %v, want nil", tc.input, err)
			}
			if gotID, want := got.ID, created.ID; gotID != want {
				t.Errorf("GetPlayerByEmail(%q) ID = %d, want %d", tc.input, gotID, want)
			}
		})
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

	created, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer)
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

	claimed, err := ps.ClaimPlayer(t.Context(), anon.ID, "alice", "alice"+"@example.test", "hash", auth.RolePlayer)
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
	// First password-bearing registrant - even via the claim path - becomes admin.
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
	if _, claimErr := ps.ClaimPlayer(
		t.Context(),
		anon.ID,
		"alice",
		"alice"+"@example.test",
		"hash",
		auth.RolePlayer,
	); claimErr != nil {
		t.Fatalf("first ClaimPlayer err = %v, want nil", claimErr)
	}

	_, err = ps.ClaimPlayer(t.Context(), anon.ID, "bob", "bob"+"@example.test", "other", auth.RolePlayer)
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

	_, err := ps.ClaimPlayer(t.Context(), 9999, "ghost", "ghost"+"@example.test", "hash", auth.RolePlayer)
	if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_ClaimPlayer_UsernameTaken(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "h", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}
	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-rival")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	_, err = ps.ClaimPlayer(t.Context(), anon.ID, "alice", "alice"+"@example.test", "h", auth.RolePlayer)
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
		// claimed its username yet - that is what makes this scenario
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

		if _, err := ps.CreatePlayer(
			t.Context(),
			"claimed",
			"claimed"+"@example.test",
			"h",
			auth.RolePlayer,
		); err != nil {
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

		credentialled, err := ps.CreatePlayer(
			t.Context(),
			"credentialled",
			"credentialled"+"@example.test",
			"h",
			auth.RolePlayer,
		)
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

// TestPlayerStore_ListPlayersByOnboardingState_AndCount pins the read
// shape that backs /admin/players (#423/#450). The list orders
// newest-first, exposes the derived has_oauth / oauth_provider flags
// + the SQL-derived onboarding_state label, and counts every row
// (including the seeded admin) when the filter is "all".
func TestPlayerStore_ListPlayersByOnboardingState_AndCount(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-list-1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	pw, err := ps.CreatePlayer(t.Context(), "pw-list-1", "pw-list-1"+"@example.test", "h", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	oauth, err := ps.CreatePlayerFromOAuth(t.Context(), "oauth-list-1", "o@example.com")
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if linkErr := ps.LinkProviderIdentity(t.Context(), oauth.ID, "google", "sub-list-1"); linkErr != nil {
		t.Fatalf("LinkProviderIdentity err = %v, want nil", linkErr)
	}

	count, err := ps.CountPlayersInOnboardingState(t.Context(), auth.OnboardingStateAll)
	if err != nil {
		t.Fatalf("CountPlayersInOnboardingState err = %v, want nil", err)
	}
	// Seeded admin + the three rows above = 4.
	if got, want := count, int64(4); got != want {
		t.Errorf("CountPlayersInOnboardingState = %d, want %d", got, want)
	}

	rows, err := ps.ListPlayersByOnboardingState(t.Context(), auth.OnboardingStateAll, 100, 0)
	if err != nil {
		t.Fatalf("ListPlayersByOnboardingState err = %v, want nil", err)
	}
	if got, want := len(rows), 4; got != want {
		t.Fatalf("ListPlayersByOnboardingState len = %d, want %d", got, want)
	}

	byID := make(map[int64]*auth.PlayerListRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	if got, want := byID[anon.ID].HasOAuth, false; got != want {
		t.Errorf("anon HasOAuth = %v, want %v", got, want)
	}
	if got, want := byID[anon.ID].HasPassword, false; got != want {
		t.Errorf("anon HasPassword = %v, want %v", got, want)
	}
	if got, want := byID[anon.ID].OnboardingState, auth.OnboardingStateAnonymous; got != want {
		t.Errorf("anon OnboardingState = %q, want %q", got, want)
	}
	if got, want := byID[pw.ID].HasPassword, true; got != want {
		t.Errorf("pw HasPassword = %v, want %v", got, want)
	}
	if got, want := byID[pw.ID].OnboardingState, auth.OnboardingStateUnverified; got != want {
		t.Errorf("pw OnboardingState = %q, want %q", got, want)
	}
	if got, want := byID[oauth.ID].HasOAuth, true; got != want {
		t.Errorf("oauth HasOAuth = %v, want %v", got, want)
	}
	if got, want := byID[oauth.ID].OAuthProvider, "google"; got != want {
		t.Errorf("oauth OAuthProvider = %q, want %q", got, want)
	}
	if got, want := byID[oauth.ID].OnboardingState, auth.OnboardingStateOAuth; got != want {
		t.Errorf("oauth OnboardingState = %q, want %q", got, want)
	}
}

// TestPlayerStore_ListPlayersByOnboardingState_FilterAndCounts pins
// the WHERE-by-state path and the GROUP BY tab counts side by side.
func TestPlayerStore_ListPlayersByOnboardingState_FilterAndCounts(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreateAnonymousPlayer(t.Context(), "anon-bucket-a"); err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err := ps.CreatePlayer(
		t.Context(),
		"pw-bucket-a",
		"pw-bucket-a@example.test",
		"h",
		auth.RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	o, err := ps.CreatePlayerFromOAuth(t.Context(), "oauth-bucket-a", "ob@example.test")
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if linkErr := ps.LinkProviderIdentity(t.Context(), o.ID, "google", "sub-bucket-a"); linkErr != nil {
		t.Fatalf("LinkProviderIdentity err = %v, want nil", linkErr)
	}

	unverifiedCount, err := ps.CountPlayersInOnboardingState(t.Context(), auth.OnboardingStateUnverified)
	if err != nil {
		t.Fatalf("CountPlayersInOnboardingState err = %v, want nil", err)
	}
	if got, want := unverifiedCount, int64(1); got != want {
		t.Errorf("unverified count = %d, want %d", got, want)
	}

	rows, err := ps.ListPlayersByOnboardingState(t.Context(), auth.OnboardingStateUnverified, 100, 0)
	if err != nil {
		t.Fatalf("ListPlayersByOnboardingState err = %v, want nil", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("unverified rows len = %d, want %d", got, want)
	}
	if got, want := rows[0].OnboardingState, auth.OnboardingStateUnverified; got != want {
		t.Errorf("row OnboardingState = %q, want %q", got, want)
	}

	counts, err := ps.CountPlayersByOnboardingState(t.Context())
	if err != nil {
		t.Fatalf("CountPlayersByOnboardingState err = %v, want nil", err)
	}
	if got, want := counts[auth.OnboardingStateAnonymous], int64(1); got != want {
		t.Errorf("anonymous bucket = %d, want %d", got, want)
	}
	if got, want := counts[auth.OnboardingStateUnverified], int64(1); got != want {
		t.Errorf("unverified bucket = %d, want %d", got, want)
	}
	if got, want := counts[auth.OnboardingStateOAuth], int64(1); got != want {
		t.Errorf("oauth bucket = %d, want %d", got, want)
	}
	// The seeded admin (id=1) has email_verified_at backfilled by an
	// earlier migration, so it lands in the verified bucket.
	if got, want := counts[auth.OnboardingStateVerified], int64(1); got != want {
		t.Errorf("verified bucket = %d, want %d", got, want)
	}
}

// TestPlayerStore_ListPlayersByOnboardingState_Pagination pins the
// LIMIT/OFFSET behaviour the admin handler relies on for ?page=N
// traversal across the "all" bucket.
func TestPlayerStore_ListPlayersByOnboardingState_Pagination(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	for i := range 5 {
		if _, err := ps.CreateAnonymousPlayer(t.Context(), fmt.Sprintf("anon-page-%d", i)); err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}
	}

	first, err := ps.ListPlayersByOnboardingState(t.Context(), auth.OnboardingStateAll, 2, 0)
	if err != nil {
		t.Fatalf("ListPlayersByOnboardingState err = %v, want nil", err)
	}
	if got, want := len(first), 2; got != want {
		t.Fatalf("first page len = %d, want %d", got, want)
	}
	second, err := ps.ListPlayersByOnboardingState(t.Context(), auth.OnboardingStateAll, 2, 2)
	if err != nil {
		t.Fatalf("ListPlayersByOnboardingState err = %v, want nil", err)
	}
	if got, want := len(second), 2; got != want {
		t.Fatalf("second page len = %d, want %d", got, want)
	}
	if first[0].ID == second[0].ID {
		t.Errorf("pages overlap: first[0]=%d, second[0]=%d", first[0].ID, second[0].ID)
	}
	// All five rows share a created_at because they were inserted in
	// the same test tick, so the tiebreaker ORDER BY p.id DESC kicks
	// in: the newest id has to land first on page 1 and page 2 has to
	// start with a strictly smaller id than page 1's tail.
	if first[0].ID < first[1].ID {
		t.Errorf("page 1 not in id-DESC order: %d before %d", first[0].ID, first[1].ID)
	}
	if first[1].ID <= second[0].ID {
		t.Errorf("page 2 starts at id %d, want < page 1 tail %d", second[0].ID, first[1].ID)
	}
}

// TestPlayerStore_ListPlayerFinishStats_NoGames asserts the short-
// circuit + zero-rows path: a brand-new player with no
// game_participants entries is absent from the result.
func TestPlayerStore_ListPlayerFinishStats_NoGames(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	p, err := ps.CreateAnonymousPlayer(t.Context(), "anon-no-games")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	stats, err := ps.ListPlayerFinishStats(t.Context(), []int64{p.ID})
	if err != nil {
		t.Fatalf("ListPlayerFinishStats err = %v, want nil", err)
	}
	if got, want := len(stats), 0; got != want {
		t.Errorf("stats len = %d, want %d", got, want)
	}

	empty, err := ps.ListPlayerFinishStats(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListPlayerFinishStats(nil) err = %v, want nil", err)
	}
	if got, want := len(empty), 0; got != want {
		t.Errorf("empty stats len = %d, want %d", got, want)
	}
}

// TestPlayerStore_SetPlayerPasswordHash_AlsoMarksUsernameClaimed pins
// the #289 fix: the operator's -reset-password CLI eventually calls
// this store method to give a player a password. Before the fix the
// SQL only updated password_hash, leaving username_claimed=0 on a
// row whose `password_hash IS NOT NULL` - which dragged the player
// client into the "claim your name" modal for a logged-in admin. The
// combined update now keeps the two columns in lockstep.
//
// After #446 SetPlayerPasswordHash matches by email (the post-446
// login credential) and the CHECK constraint on players forbids
// setting password_hash on a row whose email is NULL, so the seed
// row created below carries an email from the start.
func TestPlayerStore_SetPlayerPasswordHash_AlsoMarksUsernameClaimed(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// CreatePlayer-without-password is not exposed, so seed via the
	// OAuth helper: it inserts a row with email but no password_hash
	// and username_claimed=1 already. To exercise the
	// username_claimed=0 -> 1 flip we then rename to keep the row in
	// the "needs claim" state, then run SetPlayerPasswordHash.
	const email = "set-hash-test@example.test"
	row, err := ps.CreatePlayerFromOAuth(t.Context(), "anon-claim-after-pw", email)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	// CreatePlayerFromOAuth sets username_claimed=1; this test wants
	// the username_claimed=0 starting state, so reset it via a raw
	// UPDATE. Production code never does this; the test is exercising
	// the SetPlayerPasswordHash side effect, not the typical lifecycle.
	if _, execErr := db.ExecContext(
		t.Context(), "UPDATE players SET username_claimed = 0 WHERE id = ?", row.ID,
	); execErr != nil {
		t.Fatalf("seed UPDATE err = %v, want nil", execErr)
	}

	if setErr := ps.SetPlayerPasswordHash(t.Context(), email, "h"); setErr != nil {
		t.Fatalf("SetPlayerPasswordHash err = %v, want nil", setErr)
	}

	got, err := ps.GetPlayerByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if got.PasswordHash == "" {
		t.Error("PasswordHash empty after reset, want a non-empty hash")
	}
	if got, want := got.HasCustomName(), true; got != want {
		t.Errorf("HasCustomName() = %v, want %v (SetPlayerPasswordHash must also flip username_claimed)", got, want)
	}
}

func TestPlayerStore_SetPlayerEmail_ClearsVerification(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// CreatePlayerByAdmin stamps email_verified_at, so the row starts
	// in the verified bucket.
	created, err := ps.CreatePlayerByAdmin(
		t.Context(), "verified-then-changed", "before@example.test", "hashed-secret",
	)
	if err != nil {
		t.Fatalf("CreatePlayerByAdmin err = %v, want nil", err)
	}
	before, err := ps.GetPlayerDetail(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := before.OnboardingState, auth.OnboardingStateVerified; got != want {
		t.Fatalf("OnboardingState before = %q, want %q", got, want)
	}

	if setErr := ps.SetPlayerEmail(t.Context(), created.ID, "after@example.test"); setErr != nil {
		t.Fatalf("SetPlayerEmail err = %v, want nil", setErr)
	}

	after, err := ps.GetPlayerDetail(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := after.Email, "after@example.test"; got != want {
		t.Errorf("Email = %q, want %q", got, want)
	}
	if after.EmailVerifiedAt != nil {
		t.Errorf("EmailVerifiedAt = %v, want nil (changing the email must clear verification)", after.EmailVerifiedAt)
	}
	if got, want := after.OnboardingState, auth.OnboardingStateUnverified; got != want {
		t.Errorf("OnboardingState after = %q, want %q", got, want)
	}
}

func TestPlayerStore_ListAdminAuditForTarget_SurvivesActorDeletion(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	actor, err := ps.CreatePlayerByAdmin(t.Context(), "audit-actor", "actor@example.test", "h")
	if err != nil {
		t.Fatalf("CreatePlayerByAdmin actor err = %v, want nil", err)
	}
	target, err := ps.CreatePlayerByAdmin(t.Context(), "audit-target", "target@example.test", "h")
	if err != nil {
		t.Fatalf("CreatePlayerByAdmin target err = %v, want nil", err)
	}

	if auditErr := ps.InsertAdminAudit(
		t.Context(), actor.ID, target.ID, auth.AdminActionVerify, "{}",
	); auditErr != nil {
		t.Fatalf("InsertAdminAudit err = %v, want nil", auditErr)
	}

	// Deleting the actor must leave the target's audit row in place
	// (actor FK is ON DELETE SET NULL, not CASCADE) so the "who did
	// what" history outlives the admin account.
	if _, execErr := db.ExecContext(
		t.Context(), "DELETE FROM players WHERE id = ?", actor.ID,
	); execErr != nil {
		t.Fatalf("delete actor err = %v, want nil", execErr)
	}

	entries, err := ps.ListAdminAuditForTarget(t.Context(), target.ID, 20)
	if err != nil {
		t.Fatalf("ListAdminAuditForTarget err = %v, want nil", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("audit entry count = %d, want %d (row must survive actor deletion)", got, want)
	}
	if got, want := entries[0].ActorUsername, ""; got != want {
		t.Errorf("ActorUsername = %q, want %q (deleted actor renders blank)", got, want)
	}
	if got, want := entries[0].ActorPlayerID, int64(0); got != want {
		t.Errorf("ActorPlayerID = %d, want %d (NULL actor maps to zero)", got, want)
	}
	if got, want := entries[0].TargetPlayerID, target.ID; got != want {
		t.Errorf("TargetPlayerID = %d, want %d", got, want)
	}
}
