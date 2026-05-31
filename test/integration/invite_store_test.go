//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestInviteStore_RoundtripSingleUse covers the store-level roundtrip for
// the #318 invites table: create a pending invite, look it up live,
// consume it once (which marks it accepted), and confirm a second consume
// is rejected as single-use.
func TestInviteStore_RoundtripSingleUse(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	admin, err := stores.Players.CreatePlayer(ctx, "inv-admin", "inv-admin@example.test", "h", "admin")
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	raw, hash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := stores.Invites.CreateInvite(
		ctx, "invitee@example.test", hash, "note", admin.ID, time.Now().Add(time.Hour),
	); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}

	live, err := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(raw))
	if err != nil {
		t.Fatalf("GetLiveInvite err = %v, want nil", err)
	}
	if got, want := live.Email, "invitee@example.test"; got != want {
		t.Errorf("live.Email = %q, want %q", got, want)
	}
	if got, want := live.InvitedByPlayerID, admin.ID; got != want {
		t.Errorf("live.InvitedByPlayerID = %d, want %d", got, want)
	}

	if cerr := stores.Invites.ConsumeInvite(ctx, auth.HashInviteToken(raw)); cerr != nil {
		t.Fatalf("first ConsumeInvite err = %v, want nil", cerr)
	}

	// Second consume must be rejected (single-use), and the now-accepted
	// invite must no longer resolve as live.
	cerr := stores.Invites.ConsumeInvite(ctx, auth.HashInviteToken(raw))
	if got, want := cerr, auth.ErrInviteInvalid; !errors.Is(got, want) {
		t.Errorf("second ConsumeInvite err = %v, want %v", got, want)
	}
	_, lerr := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(raw))
	if got, want := lerr, auth.ErrInviteInvalid; !errors.Is(got, want) {
		t.Errorf("GetLiveInvite after consume err = %v, want %v", got, want)
	}
}

// TestInviteStore_ExpiredRejected pins the expires_at check: an invite
// whose expires_at is in the past neither resolves live nor consumes.
func TestInviteStore_ExpiredRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	raw, hash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := stores.Invites.CreateInvite(
		ctx, "expired@example.test", hash, "", 0, time.Now().Add(-time.Hour),
	); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}

	_, lerr := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(raw))
	if got, want := lerr, auth.ErrInviteInvalid; !errors.Is(got, want) {
		t.Errorf("GetLiveInvite expired err = %v, want %v", got, want)
	}
	cerr := stores.Invites.ConsumeInvite(ctx, auth.HashInviteToken(raw))
	if got, want := cerr, auth.ErrInviteInvalid; !errors.Is(got, want) {
		t.Errorf("ConsumeInvite expired err = %v, want %v", got, want)
	}
}

// TestInviteStore_KindSeparation pins that the per-purpose token tables do
// not cross-resolve: a password_reset_tokens hash must NOT resolve via the
// invite lookup, and an invite hash must NOT resolve via the reset lookup.
// Both tables store the same sha256-hex shape, so the separation is purely
// the table the hash was written to.
func TestInviteStore_KindSeparation(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(ctx, "kind-sep", "kind-sep@example.test", "h", "player")
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	// A reset token's hash must not resolve as a live invite.
	resetRaw, resetHash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(
		ctx, resetHash, player.ID, time.Now().Add(time.Hour),
	); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}
	_, lerr := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(resetRaw))
	if got, want := lerr, auth.ErrInviteInvalid; !errors.Is(got, want) {
		t.Errorf("reset-token hash resolved via invite lookup: err = %v, want %v", got, want)
	}

	// An invite's hash must not resolve as a live reset token.
	invRaw, invHash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := stores.Invites.CreateInvite(
		ctx, "kind-invitee@example.test", invHash, "", 0, time.Now().Add(time.Hour),
	); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}
	_, live, err := stores.ResetTokens.LookupResetToken(ctx, auth.HashResetToken(invRaw))
	if err != nil {
		t.Fatalf("LookupResetToken err = %v, want nil", err)
	}
	if live {
		t.Error("invite-token hash resolved as a live reset token; tables must not cross-resolve")
	}
}
