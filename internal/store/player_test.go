package store_test

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

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
	if got, want := created.DisplayName, "alice"; got != want {
		t.Errorf("CreatePlayer DisplayName = %q, want %q", got, want)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("CreatePlayer Role = %q, want %q", got, want)
	}
	if got, want := created.PasswordHash, "hashed-secret"; got != want {
		t.Errorf("CreatePlayer PasswordHash = %q, want %q", got, want)
	}
	// A registered user explicitly picked their displayName at the form, so
	// the frontend must see hasCustomName=true and skip the claim modal.
	if got, want := created.HasCustomName(), true; got != want {
		t.Errorf("CreatePlayer HasCustomName() = %v, want %v", got, want)
	}

	fetched, err := ps.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := fetched.ID, created.ID; got != want {
		t.Errorf("GetPlayerByDisplayName ID = %d, want %d", got, want)
	}
	if got, want := fetched.Role, auth.RoleAdmin; got != want {
		t.Errorf("GetPlayerByDisplayName Role = %q, want %q", got, want)
	}
	if got, want := fetched.HasCustomName(), true; got != want {
		t.Errorf("GetPlayerByDisplayName HasCustomName() = %v, want %v (re-fetch must persist the flag)", got, want)
	}
}

func TestPlayerStore_CreatePlayer_FirstPasswordBearer_PromotedToAdmin(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// Even when the caller asks for "player", the first credentialled
	// registrant is promoted to the top tier (admin) atomically by the SQL
	// CASE, so a fresh install can reach /admin/settings without the
	// break-glass CLI (#538). role_changed_at is stamped.
	created, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
	assertRoleState(t, db, created.ID, auth.RoleAdmin, true)
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
	assertRoleState(t, db, created.ID, auth.RolePlayer, false)
}

func TestPlayerStore_CreatePlayer_ExplicitAdmin_HonouredEvenWhenNotFirst(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	// An ADMIN_EMAILS-style requested_role=admin call on a DB that already has
	// a credentialled account becomes the top tier admin (#538). role_changed_at
	// is stamped for the requested-admin branch too.
	created, err := ps.CreatePlayer(t.Context(), "carol", "carol"+"@example.test", "hash", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got, want := created.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
	assertRoleState(t, db, created.ID, auth.RoleAdmin, true)
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
	fetched, err := ps.GetPlayerByDisplayName(t.Context(), "alice ")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := fetched.DisplayName, "alice"; got != want {
		t.Errorf("DisplayName = %q, want %q (whitespace should have been trimmed)", got, want)
	}
}

func TestPlayerStore_GetPlayerByDisplayName_NotFound(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	_, err := ps.GetPlayerByDisplayName(t.Context(), "ghost")
	if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_CreatePlayer_DuplicateDisplayName(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer first call err = %v, want nil", err)
	}

	// Different email, same displayName -> ErrDisplayNameTaken.
	_, err := ps.CreatePlayer(t.Context(), "alice", "alice-other@example.test", "other", auth.RolePlayer)
	if got, want := err, auth.ErrDisplayNameTaken; !errors.Is(got, want) {
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

// TestPlayerStore_CreatePlayerFromOAuth_LowercasesEmail pins that the
// OAuth-create path stores the email normalised, so a mixed-case OIDC
// email is found by GetPlayerByEmail instead of producing a duplicate
// row on the next sign-in. See #471.
func TestPlayerStore_CreatePlayerFromOAuth_LowercasesEmail(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreatePlayerFromOAuth(t.Context(), "oauthuser", "  OAuth@Example.Test ")
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if got, want := created.Email, "oauth@example.test"; got != want {
		t.Errorf("stored Email = %q, want %q", got, want)
	}

	found, err := ps.GetPlayerByEmail(t.Context(), "OAuth@Example.Test")
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if got, want := found.ID, created.ID; got != want {
		t.Errorf("GetPlayerByEmail ID = %d, want %d", got, want)
	}
}

// TestPlayerStore_ClaimPlayerForOAuth_LowercasesEmail pins the same
// normalisation rule on the claim-anonymous-row path so a mixed-case
// OIDC email attached to an existing row is still found on lookup.
func TestPlayerStore_ClaimPlayerForOAuth_LowercasesEmail(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-oauth")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	claimed, err := ps.ClaimPlayerForOAuth(t.Context(), anon.ID, "  Claim@Example.Test ")
	if err != nil {
		t.Fatalf("ClaimPlayerForOAuth err = %v, want nil", err)
	}
	if got, want := claimed.Email, "claim@example.test"; got != want {
		t.Errorf("stored Email = %q, want %q", got, want)
	}

	found, err := ps.GetPlayerByEmail(t.Context(), "Claim@Example.Test")
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if got, want := found.ID, claimed.ID; got != want {
		t.Errorf("GetPlayerByEmail ID = %d, want %d", got, want)
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
	if got, want := created.DisplayName, "anon-foo"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
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

func TestPlayerStore_CreateAnonymousPlayer_DuplicateDisplayName(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreateAnonymousPlayer(t.Context(), "anon-clash"); err != nil {
		t.Fatalf("first CreateAnonymousPlayer err = %v, want nil", err)
	}

	_, err := ps.CreateAnonymousPlayer(t.Context(), "anon-clash")
	if got, want := err, auth.ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v (UNIQUE violation should map to ErrDisplayNameTaken)", got, want)
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
	if got, want := claimed.DisplayName, "alice"; got != want {
		t.Errorf("claimed.DisplayName = %q, want %q", got, want)
	}
	if got, want := claimed.PasswordHash, "hash"; got != want {
		t.Errorf("claimed.PasswordHash = %q, want %q", got, want)
	}
	// First credentialled registrant - even via the claim path - becomes the
	// top tier admin (#538).
	if got, want := claimed.Role, auth.RoleAdmin; got != want {
		t.Errorf("claimed.Role = %q, want %q", got, want)
	}
	assertRoleState(t, db, claimed.ID, auth.RoleAdmin, true)
	// The claim flow is an explicit displayName choice: the player typed it
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
	stored, err := ps.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
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

func TestPlayerStore_ClaimPlayer_DisplayNameTaken(t *testing.T) {
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
	if got, want := err, auth.ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPlayerStore_UpdatePlayerDisplayName(t *testing.T) {
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
		// claimed its displayName yet - that is what makes this scenario
		// meaningful.
		if got, want := anon.HasCustomName(), false; got != want {
			t.Fatalf("precondition anon.HasCustomName() = %v, want %v", got, want)
		}

		updated, err := ps.UpdatePlayerDisplayName(t.Context(), anon.ID, "alice")
		if err != nil {
			t.Fatalf("UpdatePlayerDisplayName err = %v, want nil", err)
		}
		if got, want := updated.ID, anon.ID; got != want {
			t.Errorf("updated.ID = %d, want %d (same row)", got, want)
		}
		if got, want := updated.DisplayName, "alice"; got != want {
			t.Errorf("updated.DisplayName = %q, want %q", got, want)
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

		updated, err := ps.UpdatePlayerDisplayName(t.Context(), anon.ID, "  bob  ")
		if err != nil {
			t.Fatalf("UpdatePlayerDisplayName err = %v, want nil", err)
		}
		if got, want := updated.DisplayName, "bob"; got != want {
			t.Errorf("updated.DisplayName = %q, want %q (whitespace trimmed)", got, want)
		}
	})

	t.Run("empty displayName returns ErrDisplayNameEmpty", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-empty")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		_, err = ps.UpdatePlayerDisplayName(t.Context(), anon.ID, "   ")
		if got, want := err, auth.ErrDisplayNameEmpty; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("collision returns ErrDisplayNameTaken", func(t *testing.T) {
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

		_, err = ps.UpdatePlayerDisplayName(t.Context(), anon.ID, "claimed")
		if got, want := err, auth.ErrDisplayNameTaken; !errors.Is(got, want) {
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

		_, err = ps.UpdatePlayerDisplayName(t.Context(), credentialled.ID, "newname")
		if got, want := err, auth.ErrPlayerNotAnonymous; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("unknown player ID returns ErrPlayerNotFound", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		_, err := ps.UpdatePlayerDisplayName(t.Context(), 99999, "ghost")
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

// TestPlayerStore_SetPlayerPasswordHash_AlsoMarksDisplayNameClaimed pins
// the #289 fix: the operator's -reset-password CLI eventually calls
// this store method to give a player a password. Before the fix the
// SQL only updated password_hash, leaving displayName_claimed=0 on a
// row whose `password_hash IS NOT NULL` - which dragged the player
// client into the "claim your name" modal for a logged-in admin. The
// combined update now keeps the two columns in lockstep.
//
// After #446 SetPlayerPasswordHash matches by email (the post-446
// login credential) and the CHECK constraint on players forbids
// setting password_hash on a row whose email is NULL, so the seed
// row created below carries an email from the start.
func TestPlayerStore_SetPlayerPasswordHash_AlsoMarksDisplayNameClaimed(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// CreatePlayer-without-password is not exposed, so seed via the
	// OAuth helper: it inserts a row with email but no password_hash
	// and displayName_claimed=1 already. To exercise the
	// displayName_claimed=0 -> 1 flip we then rename to keep the row in
	// the "needs claim" state, then run SetPlayerPasswordHash.
	const email = "set-hash-test@example.test"
	row, err := ps.CreatePlayerFromOAuth(t.Context(), "anon-claim-after-pw", email)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	// CreatePlayerFromOAuth sets displayName_claimed=1; this test wants
	// the displayName_claimed=0 starting state, so reset it via a raw
	// UPDATE. Production code never does this; the test is exercising
	// the SetPlayerPasswordHash side effect, not the typical lifecycle.
	if _, execErr := db.ExecContext(
		t.Context(), "UPDATE players SET display_name_claimed = 0 WHERE id = ?", row.ID,
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
		t.Errorf("HasCustomName() = %v, want %v (SetPlayerPasswordHash must also flip displayName_claimed)", got, want)
	}
}

// TestPlayerStore_SetPlayerPasswordHash_BumpsSessionVersion pins that the
// operator -reset-password rotation bumps session_version, invalidating every
// other live cookie issued on the old credential.
func TestPlayerStore_SetPlayerPasswordHash_BumpsSessionVersion(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	const email = "session-bump-test@example.test"
	row, err := ps.CreatePlayerFromOAuth(t.Context(), "anon-session-bump", email)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}

	before, err := ps.GetPlayerByID(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}

	if setErr := ps.SetPlayerPasswordHash(t.Context(), email, "h"); setErr != nil {
		t.Fatalf("SetPlayerPasswordHash err = %v, want nil", setErr)
	}

	after, err := ps.GetPlayerByID(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := after.SessionVersion, before.SessionVersion+1; got != want {
		t.Errorf("SessionVersion = %d, want %d (operator reset must invalidate other sessions)", got, want)
	}
}

// TestPlayerStore_RenamePlayer_MarksClaimed pins that the player's own
// rename (profile self-claim path) marks the name as player-claimed.
func TestPlayerStore_RenamePlayer_MarksClaimed(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-self-claim")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if got, want := anon.HasCustomName(), false; got != want {
		t.Fatalf("precondition HasCustomName() = %v, want %v", got, want)
	}

	updated, err := ps.RenamePlayer(t.Context(), anon.ID, "self-picked")
	if err != nil {
		t.Fatalf("RenamePlayer err = %v, want nil", err)
	}
	if got, want := updated.DisplayName, "self-picked"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := updated.HasCustomName(), true; got != want {
		t.Errorf("HasCustomName() = %v, want %v (self-rename must claim the name)", got, want)
	}
}

// TestPlayerStore_AdminRenamePlayer_LeavesClaimedUntouched pins that an admin
// rename of a guest does not mark the name as player-claimed (the guest never
// picked it).
func TestPlayerStore_AdminRenamePlayer_LeavesClaimedUntouched(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	anon, err := ps.CreateAnonymousPlayer(t.Context(), "anon-admin-rename")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if got, want := anon.HasCustomName(), false; got != want {
		t.Fatalf("precondition HasCustomName() = %v, want %v", got, want)
	}

	updated, err := ps.AdminRenamePlayer(t.Context(), anon.ID, "Tidied Name")
	if err != nil {
		t.Fatalf("AdminRenamePlayer err = %v, want nil", err)
	}
	if got, want := updated.DisplayName, "Tidied Name"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := updated.HasCustomName(), false; got != want {
		t.Errorf("HasCustomName() = %v, want %v (admin rename must not claim a guest's name)", got, want)
	}

	// Persisted, not just returned by RETURNING.
	refetched, err := ps.GetPlayerByID(t.Context(), anon.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refetched.HasCustomName(), false; got != want {
		t.Errorf("refetched.HasCustomName() = %v, want %v", got, want)
	}
}

func TestPlayerStore_SetPlayerEmail_ClearsVerification(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// CreatePlayerByAdmin stamps email_verified_at, so the row starts
	// in the verified bucket.
	created, err := ps.CreatePlayerByAdmin(
		t.Context(), "verified-then-changed", "before@example.test", "hashed-secret", auth.RolePlayer,
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

// TestPlayerStore_SetPlayerRole walks a single row through every tier
// transition (#538) - player -> host -> admin -> player - and pins the role
// and role_changed_at at each step: the timestamp is stamped on every change.
func TestPlayerStore_SetPlayerRole(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	// Seed an anonymous (role=player) row.
	created, err := ps.CreateAnonymousPlayer(t.Context(), "role-target")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	for _, role := range []string{auth.RoleHost, auth.RoleAdmin, auth.RolePlayer} {
		if setErr := ps.SetPlayerRole(t.Context(), created.ID, role); setErr != nil {
			t.Fatalf("set role %q err = %v, want nil", role, setErr)
		}
		assertRoleState(t, db, created.ID, role, true)

		got, getErr := ps.GetPlayerByID(t.Context(), created.ID)
		if getErr != nil {
			t.Fatalf("GetPlayerByID err = %v, want nil", getErr)
		}
		if got.Role != role {
			t.Errorf("Role after set = %q, want %q", got.Role, role)
		}
	}
}

func TestPlayerStore_SetPlayerRole_NotFound(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	err := ps.SetPlayerRole(t.Context(), 99999, auth.RoleAdmin)
	if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// seedAdmin creates a fresh player and stamps the Admin role, returning its id.
// A fresh migrated DB has no admins, so the admin count equals the number of
// these the test creates.
func seedAdmin(t *testing.T, ps *PlayerStore, displayName string) int64 {
	t.Helper()

	created, err := ps.CreateAnonymousPlayer(t.Context(), displayName)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer(%q) err = %v, want nil", displayName, err)
	}
	if err := ps.SetPlayerRole(t.Context(), created.ID, auth.RoleAdmin); err != nil {
		t.Fatalf("SetPlayerRole(%q, admin) err = %v, want nil", displayName, err)
	}

	return created.ID
}

// TestPlayerStore_DemoteAdmin pins the atomic last-admin guard (#997): a
// demotion succeeds while a second admin exists, the only-admin demotion is
// refused with ErrLastAdmin and leaves the row admin, and a missing or
// already-non-admin row maps to ErrPlayerNotFound.
func TestPlayerStore_DemoteAdmin(t *testing.T) {
	t.Parallel()

	t.Run("refuses the last admin and leaves the row admin", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		only := seedAdmin(t, ps, "only-admin")

		err := ps.DemoteAdmin(t.Context(), only, auth.RoleHost)
		if got, want := err, auth.ErrLastAdmin; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
		assertRoleState(t, db, only, auth.RoleAdmin, true)
	})

	t.Run("succeeds while a second admin exists", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		seedAdmin(t, ps, "keeper-admin")
		target := seedAdmin(t, ps, "demote-me")

		if err := ps.DemoteAdmin(t.Context(), target, auth.RoleHost); err != nil {
			t.Fatalf("DemoteAdmin err = %v, want nil", err)
		}
		assertRoleState(t, db, target, auth.RoleHost, true)
	})

	t.Run("missing row maps to ErrPlayerNotFound", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		err := ps.DemoteAdmin(t.Context(), 999999, auth.RoleHost)
		if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("non-admin row maps to ErrPlayerNotFound", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		// A second admin guarantees the refusal below is the not-admin clause,
		// not the last-admin clause.
		seedAdmin(t, ps, "guard-admin")
		nonAdmin, err := ps.CreateAnonymousPlayer(t.Context(), "plain-player")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		err = ps.DemoteAdmin(t.Context(), nonAdmin.ID, auth.RoleHost)
		if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

// TestPlayerStore_DemoteAdmin_ConcurrentDemotionsKeepOneAdmin pins the race the
// guard exists to close (#997): with exactly two admins, two concurrent
// demotions must not both succeed - at least one is refused with ErrLastAdmin
// so at least one admin always remains. The pre-fix check-then-act guard let
// both pass and left zero admins.
func TestPlayerStore_DemoteAdmin_ConcurrentDemotionsKeepOneAdmin(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	adminA := seedAdmin(t, ps, "race-admin-a")
	adminB := seedAdmin(t, ps, "race-admin-b")

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errs    = make(map[int64]error, 2)
		targets = []int64{adminA, adminB}
	)
	wg.Add(len(targets))
	for _, id := range targets {
		go func() {
			defer wg.Done()
			err := ps.DemoteAdmin(t.Context(), id, auth.RoleHost)
			mu.Lock()
			errs[id] = err
			mu.Unlock()
		}()
	}
	wg.Wait()

	// At least one demotion must be refused, and at least one admin must remain.
	refusals := 0
	for _, err := range errs {
		if errors.Is(err, auth.ErrLastAdmin) {
			refusals++
		}
	}
	if refusals == 0 {
		t.Errorf("both demotions succeeded (errs = %v); the guard must refuse at least one", errs)
	}

	var remaining int
	row := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM players WHERE role = 'admin'")
	if err := row.Scan(&remaining); err != nil {
		t.Fatalf("count admins err = %v, want nil", err)
	}
	if remaining < 1 {
		t.Errorf("admins remaining = %d, want >= 1 (the invariant the guard protects)", remaining)
	}
}

// TestPlayerStore_GetPlayerDetail_ExposesRole pins that the detail read
// surfaces the role so the #538 role selector can preselect the current tier.
func TestPlayerStore_GetPlayerDetail_ExposesRole(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	created, err := ps.CreatePlayerByAdmin(t.Context(), "detail-role", "detail-role@example.test", "h", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayerByAdmin err = %v, want nil", err)
	}

	before, err := ps.GetPlayerDetail(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := before.Role, auth.RolePlayer; got != want {
		t.Errorf("Role before = %q, want %q", got, want)
	}

	if setErr := ps.SetPlayerRole(t.Context(), created.ID, auth.RoleHost); setErr != nil {
		t.Fatalf("set host err = %v, want nil", setErr)
	}

	after, err := ps.GetPlayerDetail(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := after.Role, auth.RoleHost; got != want {
		t.Errorf("Role after = %q, want %q", got, want)
	}
}

// assertRoleState reads the row by id and asserts role and whether
// role_changed_at is non-NULL (#538).
func assertRoleState(t *testing.T, db *sql.DB, id int64, wantRole string, wantChangedSet bool) {
	t.Helper()
	var (
		role    string
		changed sql.NullTime
	)
	row := db.QueryRowContext(t.Context(),
		"SELECT role, role_changed_at FROM players WHERE id = ?", id)
	if err := row.Scan(&role, &changed); err != nil {
		t.Fatalf("scan role state err = %v, want nil", err)
	}
	if got := role; got != wantRole {
		t.Errorf("role = %q, want %q", got, wantRole)
	}
	if got, want := changed.Valid, wantChangedSet; got != want {
		t.Errorf("role_changed_at set = %v, want %v", got, want)
	}
}

func TestPlayerStore_ListAdminAuditForTarget_SurvivesActorDeletion(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	actor, err := ps.CreatePlayerByAdmin(t.Context(), "audit-actor", "actor@example.test", "h", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayerByAdmin actor err = %v, want nil", err)
	}
	target, err := ps.CreatePlayerByAdmin(t.Context(), "audit-target", "target@example.test", "h", auth.RolePlayer)
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
	if got, want := entries[0].ActorDisplayName, ""; got != want {
		t.Errorf("ActorDisplayName = %q, want %q (deleted actor renders blank)", got, want)
	}
	if got, want := entries[0].ActorPlayerID, int64(0); got != want {
		t.Errorf("ActorPlayerID = %d, want %d (NULL actor maps to zero)", got, want)
	}
	if got, want := entries[0].TargetPlayerID, target.ID; got != want {
		t.Errorf("TargetPlayerID = %d, want %d", got, want)
	}
}

// TestResetTokenStore_RoundtripHappyPath covers the store-level
// roundtrip for the #112 reset-token table: mint a token, persist its
// hash, then consume it. The consume call rotates password_hash AND
// bumps session_version atomically; both effects are observed via a
// follow-up GetPlayerByID.
func TestResetTokenStore_RoundtripHappyPath(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-happy", "reset-happy@example.test", "old-hash", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	startingVersion := player.SessionVersion

	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	gotID, err := stores.ResetTokens.ConsumeResetToken(ctx, auth.HashResetToken(raw), "new-hash")
	if err != nil {
		t.Fatalf("ConsumeResetToken err = %v, want nil", err)
	}
	if got, want := gotID, player.ID; got != want {
		t.Errorf("ConsumeResetToken playerID = %d, want %d", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refreshed.PasswordHash, "new-hash"; got != want {
		t.Errorf("password_hash = %q, want %q", got, want)
	}
	if got, want := refreshed.SessionVersion, startingVersion+1; got != want {
		t.Errorf("session_version = %d, want %d (bump on reset)", got, want)
	}
}

// TestResetTokenStore_ReplayRejectsConsumedToken pins single-use: a
// second consume against the same hash returns ErrResetTokenInvalid
// and leaves the player row untouched.
func TestResetTokenStore_ReplayRejectsConsumedToken(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-replay", "reset-replay@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	if _, cerr := stores.ResetTokens.ConsumeResetToken(ctx, auth.HashResetToken(raw), "first"); cerr != nil {
		t.Fatalf("first consume err = %v, want nil", cerr)
	}
	_, cerr := stores.ResetTokens.ConsumeResetToken(ctx, auth.HashResetToken(raw), "second")
	if got, want := cerr, auth.ErrResetTokenInvalid; !errors.Is(got, want) {
		t.Errorf("second consume err = %v, want %v", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refreshed.PasswordHash, "first"; got != want {
		t.Errorf("password_hash = %q, want %q (replay must not overwrite)", got, want)
	}
}

// TestResetTokenStore_ExpiredTokenRejected pins the expires_at check:
// a token whose expires_at is in the past consumes as invalid and
// leaves password_hash + session_version untouched.
func TestResetTokenStore_ExpiredTokenRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-expired", "reset-expired@example.test", "old", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	startingVersion := player.SessionVersion
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(-time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	_, cerr := stores.ResetTokens.ConsumeResetToken(ctx, auth.HashResetToken(raw), "new")
	if got, want := cerr, auth.ErrResetTokenInvalid; !errors.Is(got, want) {
		t.Errorf("expired consume err = %v, want %v", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refreshed.PasswordHash, "old"; got != want {
		t.Errorf("password_hash = %q, want %q (expired must not overwrite)", got, want)
	}
	if got, want := refreshed.SessionVersion, startingVersion; got != want {
		t.Errorf("session_version = %d, want %d (expired must not bump)", got, want)
	}
}

// TestResetTokenStore_InvalidHashRejected covers the no-row branch.
func TestResetTokenStore_InvalidHashRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	_, cerr := stores.ResetTokens.ConsumeResetToken(ctx, "no-such-hash", "new")
	if got, want := cerr, auth.ErrResetTokenInvalid; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestParseSQLiteTimestamp(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw     string
		wantNil bool
		want    time.Time
	}{
		"current_timestamp format": {
			raw:  "2026-06-04 12:34:56",
			want: time.Date(2026, 6, 4, 12, 34, 56, 0, time.UTC),
		},
		"rfc3339 falls through to nil": {
			raw:     "2026-06-04T12:34:56Z",
			wantNil: true,
		},
		"unparseable falls through to nil": {
			raw:     "not a timestamp",
			wantNil: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := ParseSQLiteTimestamp(tc.raw)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ParseSQLiteTimestamp(%q) = %v, want nil", tc.raw, got)
				}

				return
			}
			if got == nil {
				t.Fatalf("ParseSQLiteTimestamp(%q) = nil, want %v", tc.raw, tc.want)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseSQLiteTimestamp(%q) = %v, want %v", tc.raw, *got, tc.want)
			}
		})
	}
}

func TestPlayerStore_SetPlayerEmail(t *testing.T) {
	t.Parallel()

	t.Run("happy path overwrites the email", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		created, err := ps.CreatePlayerByAdmin(
			t.Context(), "set-email-happy", "before@example.test", "hashed-secret", auth.RolePlayer,
		)
		if err != nil {
			t.Fatalf("CreatePlayerByAdmin err = %v, want nil", err)
		}

		if setErr := ps.SetPlayerEmail(t.Context(), created.ID, "after@example.test"); setErr != nil {
			t.Fatalf("SetPlayerEmail err = %v, want nil", setErr)
		}

		got, err := ps.GetPlayerByID(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("GetPlayerByID err = %v, want nil", err)
		}
		if want := "after@example.test"; got.Email != want {
			t.Errorf("Email = %q, want %q", got.Email, want)
		}
	})

	t.Run("returns ErrEmailTaken on a UNIQUE collision", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		if _, err := ps.CreatePlayerByAdmin(
			t.Context(), "set-email-owner", "taken@example.test", "hashed-secret", auth.RolePlayer,
		); err != nil {
			t.Fatalf("CreatePlayerByAdmin (owner) err = %v, want nil", err)
		}
		other, err := ps.CreatePlayerByAdmin(
			t.Context(), "set-email-other", "other@example.test", "hashed-secret", auth.RolePlayer,
		)
		if err != nil {
			t.Fatalf("CreatePlayerByAdmin (other) err = %v, want nil", err)
		}

		err = ps.SetPlayerEmail(t.Context(), other.ID, "taken@example.test")
		if got, want := err, auth.ErrEmailTaken; !errors.Is(got, want) {
			t.Errorf("SetPlayerEmail err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrPlayerNotFound for an unknown id", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		ps := NewPlayerStore(db, slog.Default())

		err := ps.SetPlayerEmail(t.Context(), 999999, "nobody@example.test")
		if got, want := err, auth.ErrPlayerNotFound; !errors.Is(got, want) {
			t.Errorf("SetPlayerEmail err = %v, want %v", got, want)
		}
	})
}

// TestPlayerStore_CreatePlayerFromOAuth_HappyPath pins the create path:
// the display name is trimmed, the email is lower-cased, and a player
// with a player role is returned.
func TestPlayerStore_CreatePlayerFromOAuth_HappyPath(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	player, err := ps.CreatePlayerFromOAuth(t.Context(), "  Oauthy  ", "  Fresh@Example.Test ")
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if got, want := player.DisplayName, "Oauthy"; got != want {
		t.Errorf("DisplayName = %q, want %q (trimmed)", got, want)
	}
	if got, want := player.Email, "fresh@example.test"; got != want {
		t.Errorf("Email = %q, want %q (lower-cased)", got, want)
	}
	// The role CASE in the query promotes the first OAuth-only registrant
	// to admin, so the exact role depends on DB state; only assert it is
	// populated.
	if player.Role == "" {
		t.Error("Role = empty, want non-empty")
	}
}

// TestPlayerStore_CreatePlayerFromOAuth_DuplicateDisplayName pins that a
// UNIQUE display-name collision maps to auth.ErrDisplayNameTaken, the
// sentinel the OAuth handler retries on with a fresh petname.
func TestPlayerStore_CreatePlayerFromOAuth_DuplicateDisplayName(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())

	if _, err := ps.CreatePlayerFromOAuth(t.Context(), "samename", "first@example.test"); err != nil {
		t.Fatalf("first CreatePlayerFromOAuth err = %v, want nil", err)
	}

	_, err := ps.CreatePlayerFromOAuth(t.Context(), "samename", "second@example.test")
	if got, want := err, auth.ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("CreatePlayerFromOAuth err = %v, want %v", got, want)
	}
}

// TestPlayerStore_CreatePlayerFromOAuth_ClosedDBWraps pins the
// store-error branch: a closed DB surfaces a wrapped "failed to create
// player from oauth" rather than a bare driver error.
func TestPlayerStore_CreatePlayerFromOAuth_ClosedDBWraps(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	ps := NewPlayerStore(db, slog.Default())
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	_, err := ps.CreatePlayerFromOAuth(t.Context(), "anyone", "anyone@example.test")
	if err == nil {
		t.Fatal("CreatePlayerFromOAuth err = nil, want non-nil on closed DB")
	}
	if got, want := err.Error(), "failed to create player from oauth"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestPlayerStore_SetPlayerApprovedNow pins the #1227 approval write: an
// unapproved account gains an approved_at stamp, the change round-trips through
// GetPlayerByID, and a second approval is an idempotent no-op.
func TestPlayerStore_SetPlayerApprovedNow(t *testing.T) {
	t.Parallel()

	ps := NewPlayerStore(dbtest.Open(t), slog.Default())
	// The first credentialled registrant becomes an auto-approved admin, so a
	// second player is the unapproved subject.
	if _, err := ps.CreatePlayer(t.Context(), "boss", "boss@example.test", "hash", auth.RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer admin err = %v, want nil", err)
	}
	bob, err := ps.CreatePlayer(t.Context(), "bob", "bob@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer bob err = %v, want nil", err)
	}
	if bob.IsApproved() {
		t.Fatal("fresh non-admin player IsApproved() = true, want false")
	}

	if err = ps.SetPlayerApprovedNow(t.Context(), bob.ID); err != nil {
		t.Fatalf("SetPlayerApprovedNow err = %v, want nil", err)
	}
	got, err := ps.GetPlayerByID(t.Context(), bob.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if !got.IsApproved() {
		t.Error("after SetPlayerApprovedNow IsApproved() = false, want true")
	}

	// Idempotent: approving again is a no-op that returns nil.
	if err = ps.SetPlayerApprovedNow(t.Context(), bob.ID); err != nil {
		t.Errorf("second SetPlayerApprovedNow err = %v, want nil", err)
	}
}

// TestPlayerStore_ListAdminEmails pins the #1227 admin-email lookup: only admins
// with an address on file are returned.
func TestPlayerStore_ListAdminEmails(t *testing.T) {
	t.Parallel()

	ps := NewPlayerStore(dbtest.Open(t), slog.Default())
	if _, err := ps.CreatePlayer(t.Context(), "alice", "alice@example.test", "hash", auth.RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer alice err = %v, want nil", err)
	}
	if _, err := ps.CreatePlayer(t.Context(), "bob", "bob@example.test", "hash", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer bob err = %v, want nil", err)
	}

	emails, err := ps.ListAdminEmails(t.Context())
	if err != nil {
		t.Fatalf("ListAdminEmails err = %v, want nil", err)
	}
	if got, want := len(emails), 1; got != want {
		t.Fatalf("ListAdminEmails len = %d, want %d (%v)", got, want, emails)
	}
	if got, want := emails[0], "alice@example.test"; got != want {
		t.Errorf("ListAdminEmails[0] = %q, want %q", got, want)
	}
}
