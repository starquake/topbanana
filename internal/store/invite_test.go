//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/store"
)

// TestInviteStore_RoundtripSingleUse covers the store-level roundtrip for
// the #318 invites table: create a pending invite, look it up live,
// consume it once (which marks it accepted), and confirm a second consume
// is rejected as single-use.
func TestInviteStore_RoundtripSingleUse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

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

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

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

// TestInviteStore_ListPendingInvites covers the management list query
// (#318): a pending invite appears with its inviter displayName resolved via
// the LEFT JOIN, while accepted and revoked invites are excluded.
func TestInviteStore_ListPendingInvites(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	admin, err := stores.Players.CreatePlayer(ctx, "list-admin", "list-admin@example.test", "h", "admin")
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	mintInvite(ctx, t, stores.Invites, "list-pending@example.test", time.Now().Add(time.Hour))
	// Attribute one invite to the admin so the inviter displayName resolves.
	_, attrHash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := stores.Invites.CreateInvite(
		ctx, "list-attributed@example.test", attrHash, "", admin.ID, time.Now().Add(time.Hour),
	); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}
	// A revoked invite must not appear.
	mintInvite(ctx, t, stores.Invites, "list-revoked@example.test", time.Now().Add(time.Hour))
	revokedID := inviteIDForEmail(ctx, t, db, "list-revoked@example.test")
	if rerr := stores.Invites.RevokeInvite(ctx, revokedID); rerr != nil {
		t.Fatalf("RevokeInvite err = %v, want nil", rerr)
	}

	pending, err := stores.Invites.ListPendingInvites(ctx)
	if err != nil {
		t.Fatalf("ListPendingInvites err = %v, want nil", err)
	}

	byEmail := map[string]*auth.PendingInvite{}
	for _, p := range pending {
		byEmail[p.Email] = p
	}
	if _, ok := byEmail["list-pending@example.test"]; !ok {
		t.Error("pending invite missing from list")
	}
	if _, ok := byEmail["list-revoked@example.test"]; ok {
		t.Error("revoked invite must not appear in the pending list")
	}
	attr, ok := byEmail["list-attributed@example.test"]
	if !ok {
		t.Fatal("attributed invite missing from list")
	}
	if got, want := attr.InviterDisplayName, "list-admin"; got != want {
		t.Errorf("attr.InviterDisplayName = %q, want %q", got, want)
	}
}

// TestInviteStore_RevokeNotPending pins that revoking an already-revoked or
// non-existent invite returns ErrInviteNotPending rather than a wrapped
// no-rows error or a silent success.
func TestInviteStore_RevokeNotPending(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	mintInvite(ctx, t, stores.Invites, "revoke-twice@example.test", time.Now().Add(time.Hour))
	id := inviteIDForEmail(ctx, t, db, "revoke-twice@example.test")
	if err := stores.Invites.RevokeInvite(ctx, id); err != nil {
		t.Fatalf("first RevokeInvite err = %v, want nil", err)
	}
	if got, want := stores.Invites.RevokeInvite(ctx, id), auth.ErrInviteNotPending; !errors.Is(got, want) {
		t.Errorf("second RevokeInvite err = %v, want %v", got, want)
	}
	if got, want := stores.Invites.RevokeInvite(ctx, 999999), auth.ErrInviteNotPending; !errors.Is(got, want) {
		t.Errorf("RevokeInvite(missing) err = %v, want %v", got, want)
	}
}

// TestInviteStore_RotateInviteToken proves the resend rotation at the store
// layer: the old hash stops resolving, the freshly minted hash resolves
// live, and rotating a non-pending id returns ErrInviteNotPending.
func TestInviteStore_RotateInviteToken(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

	oldRaw := mintInvite(ctx, t, stores.Invites, "rotate@example.test", time.Now().Add(time.Hour))
	id := inviteIDForEmail(ctx, t, db, "rotate@example.test")

	newRaw, newHash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	email, err := stores.Invites.RotateInviteToken(ctx, id, newHash, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RotateInviteToken err = %v, want nil", err)
	}
	if got, want := email, "rotate@example.test"; got != want {
		t.Errorf("RotateInviteToken email = %q, want %q", got, want)
	}

	// The old link is dead.
	_, oldErr := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(oldRaw))
	if !errors.Is(oldErr, auth.ErrInviteInvalid) {
		t.Errorf("old token GetLiveInvite err = %v, want ErrInviteInvalid", oldErr)
	}
	// The new link is live.
	if _, lerr := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(newRaw)); lerr != nil {
		t.Errorf("new token GetLiveInvite err = %v, want nil", lerr)
	}

	// Rotating a consumed (non-pending) invite is rejected.
	if cerr := stores.Invites.ConsumeInvite(ctx, auth.HashInviteToken(newRaw)); cerr != nil {
		t.Fatalf("ConsumeInvite err = %v, want nil", cerr)
	}
	_, rerr := stores.Invites.RotateInviteToken(ctx, id, newHash, time.Now().Add(time.Hour))
	if !errors.Is(rerr, auth.ErrInviteNotPending) {
		t.Errorf("RotateInviteToken(consumed) err = %v, want %v", rerr, auth.ErrInviteNotPending)
	}
}

// TestInviteStore_KindSeparation pins that the per-purpose token tables do
// not cross-resolve: a password_reset_tokens hash must NOT resolve via the
// invite lookup, and an invite hash must NOT resolve via the reset lookup.
// Both tables store the same sha256-hex shape, so the separation is purely
// the table the hash was written to.
func TestInviteStore_KindSeparation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)
	stores := New(db, slog.Default())

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

// mintInvite creates a pending invite directly through the store and returns
// the raw token (the only place the raw value lives, since CreateInvite
// persists only the hash).
func mintInvite(
	ctx context.Context, t *testing.T, invites auth.InviteStore, email string, expiresAt time.Time,
) string {
	t.Helper()
	raw, hash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := invites.CreateInvite(ctx, email, hash, "", 0, expiresAt); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}

	return raw
}

// inviteIDForEmail returns the id of the (single) invite row for email.
func inviteIDForEmail(ctx context.Context, t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	row := db.QueryRowContext(ctx, "SELECT id FROM invites WHERE email = ? ORDER BY id DESC LIMIT 1", email)
	if err := row.Scan(&id); err != nil {
		t.Fatalf("inviteIDForEmail scan err = %v, want nil", err)
	}

	return id
}
