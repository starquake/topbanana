//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestResetTokenStore_RoundtripHappyPath covers the store-level
// roundtrip for the #112 reset-token table: mint a token, persist its
// hash, then consume it. The consume call rotates password_hash AND
// bumps session_version atomically; both effects are observed via a
// follow-up GetPlayerByID.
func TestResetTokenStore_RoundtripHappyPath(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

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

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

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

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

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

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	_, cerr := stores.ResetTokens.ConsumeResetToken(ctx, "no-such-hash", "new")
	if got, want := cerr, auth.ErrResetTokenInvalid; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}
