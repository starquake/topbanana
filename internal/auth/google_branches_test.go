package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

// identityStubStore is a fault-injection OAuthIdentityStore: each method
// returns a per-field configured value/error. The race-recovery branches
// in linkExistingPlayerByEmail and claimAnonymousSessionPlayer fire only
// when two concurrent OAuth callbacks interleave, which a single-threaded
// real DB cannot reproduce on demand, so the result of each call is
// injected here. CreatePlayerFromOAuth is never reached by either helper
// under test, so it stays hard-wired to ErrUnsupported to surface an
// unexpected call loudly.
type identityStubStore struct {
	byEmailPlayer   *Player
	byEmailErr      error
	bySubjectPlayer *Player
	bySubjectErr    error
	linkErr         error
	markVerifiedErr error
	claimPlayer     *Player
	claimErr        error
}

func (s *identityStubStore) GetPlayerByEmail(_ context.Context, _ string) (*Player, error) {
	return s.byEmailPlayer, s.byEmailErr
}

func (s *identityStubStore) GetPlayerByProviderSubject(_ context.Context, _, _ string) (*Player, error) {
	return s.bySubjectPlayer, s.bySubjectErr
}

func (s *identityStubStore) LinkProviderIdentity(_ context.Context, _ int64, _, _ string) error {
	return s.linkErr
}

func (s *identityStubStore) MarkPlayerEmailVerifiedIfNew(_ context.Context, _ int64) error {
	return s.markVerifiedErr
}

func (s *identityStubStore) ClaimPlayerForOAuth(_ context.Context, _ int64, _ string) (*Player, error) {
	return s.claimPlayer, s.claimErr
}

func (*identityStubStore) CreatePlayerFromOAuth(_ context.Context, _, _ string) (*Player, error) {
	return nil, errors.ErrUnsupported
}

// TestLinkExistingPlayerByEmail_GetPlayerByEmailErrorWraps pins branch
// (a): a GetPlayerByEmail failure that is not ErrPlayerNotFound wraps as
// "get player by email" rather than being swallowed as a no-match.
func TestLinkExistingPlayerByEmail_GetPlayerByEmailErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{byEmailErr: errors.New("db down")}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), store, "sub", "x@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "get player by email"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestLinkExistingPlayerByEmail_RecoversFromConcurrentLink pins branch
// (b): GetPlayerByEmail finds a row, LinkProviderIdentity loses the race
// with ErrIdentityAlreadyLinked, and the refetch by subject returns the
// winning row.
func TestLinkExistingPlayerByEmail_RecoversFromConcurrentLink(t *testing.T) {
	t.Parallel()

	winner := &Player{ID: 42, DisplayName: "alice", Email: "alice@example.test"}
	store := &identityStubStore{
		byEmailPlayer:   &Player{ID: 7, DisplayName: "alice", Email: "alice@example.test"},
		linkErr:         ErrIdentityAlreadyLinked,
		bySubjectPlayer: winner,
	}

	got, err := ExportLinkExistingPlayerByEmail(t.Context(), store, "sub", "alice@example.test")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil || got.ID != winner.ID {
		t.Errorf("recovered player = %v, want id %d (the winner's row)", got, winner.ID)
	}
}

// TestLinkExistingPlayerByEmail_RefetchAfterLinkRaceErrorWraps pins
// branch (c): the link race fires but the refetch by subject errors, so
// the failure wraps as "refetch after link race".
func TestLinkExistingPlayerByEmail_RefetchAfterLinkRaceErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{
		byEmailPlayer: &Player{ID: 7, Email: "alice@example.test"},
		linkErr:       ErrIdentityAlreadyLinked,
		bySubjectErr:  errors.New("refetch failed"),
	}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), store, "sub", "alice@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "refetch after link race"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestLinkExistingPlayerByEmail_MarkVerifiedErrorWraps pins branch (d):
// the link succeeds but MarkPlayerEmailVerifiedIfNew errors, so the
// failure wraps as "mark email verified after link".
func TestLinkExistingPlayerByEmail_MarkVerifiedErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{
		byEmailPlayer:   &Player{ID: 7, Email: "alice@example.test"},
		markVerifiedErr: errors.New("stamp failed"),
	}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), store, "sub", "alice@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "mark email verified after link"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestLinkExistingPlayerByEmail_StampsVerifiedWhenNil pins branch (e):
// the happy path links the identity and stamps EmailVerifiedAt on a row
// whose column was still nil. The non-nil EmailVerifiedAt confirms the
// helper ran past the MarkPlayerEmailVerifiedIfNew call to the stamp.
func TestLinkExistingPlayerByEmail_StampsVerifiedWhenNil(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{byEmailPlayer: &Player{ID: 7, Email: "alice@example.test"}}

	got, err := ExportLinkExistingPlayerByEmail(t.Context(), store, "sub", "alice@example.test")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.EmailVerifiedAt == nil {
		t.Error("EmailVerifiedAt = nil, want non-nil (stamped when previously nil)")
	}
}

// TestClaimAnonymousSessionPlayer_LookupAfterClaimFindsRow pins branch
// (a): the claim reports ErrPlayerNotFound, but a concurrent callback
// already linked the (provider, subject), so the lookup by subject
// returns that existing row.
func TestClaimAnonymousSessionPlayer_LookupAfterClaimFindsRow(t *testing.T) {
	t.Parallel()

	winner := &Player{ID: 99, Email: "winner@example.test"}
	store := &identityStubStore{
		claimErr:        ErrPlayerNotFound,
		bySubjectPlayer: winner,
	}

	got, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "x@example.test")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil || got.ID != winner.ID {
		t.Errorf("recovered player = %v, want id %d (the winner's row)", got, winner.ID)
	}
}

// TestClaimAnonymousSessionPlayer_LookupAfterClaimErrorWraps pins branch
// (b): the claim reports ErrPlayerNotFound and the recovery lookup
// errors with something other than ErrPlayerNotFound, so the failure
// wraps as "lookup after claim race".
func TestClaimAnonymousSessionPlayer_LookupAfterClaimErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{
		claimErr:     ErrPlayerNotFound,
		bySubjectErr: errors.New("lookup blew up"),
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "x@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "lookup after claim race"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestClaimAnonymousSessionPlayer_NoConcurrentLinkReturnsNotFound pins
// branch (c): the claim reports ErrPlayerNotFound and no concurrent
// callback linked the subject, so the helper returns ErrPlayerNotFound
// for the caller to fall through to createGooglePlayer.
func TestClaimAnonymousSessionPlayer_NoConcurrentLinkReturnsNotFound(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{
		claimErr:     ErrPlayerNotFound,
		bySubjectErr: ErrPlayerNotFound,
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "x@example.test")
	if got, want := err, ErrPlayerNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// TestClaimAnonymousSessionPlayer_LinkRaceRefetchesRow pins branch (d):
// the claim succeeds but LinkProviderIdentity loses the race with
// ErrIdentityAlreadyLinked, so the refetch by subject returns the
// canonical OAuth-linked row.
func TestClaimAnonymousSessionPlayer_LinkRaceRefetchesRow(t *testing.T) {
	t.Parallel()

	winner := &Player{ID: 99, Email: "winner@example.test"}
	store := &identityStubStore{
		claimPlayer:     &Player{ID: 7, Email: "claimed@example.test"},
		linkErr:         ErrIdentityAlreadyLinked,
		bySubjectPlayer: winner,
	}

	got, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "claimed@example.test")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil || got.ID != winner.ID {
		t.Errorf("recovered player = %v, want id %d (the winner's row)", got, winner.ID)
	}
}

// TestClaimAnonymousSessionPlayer_LinkRaceRefetchErrorWraps pins branch
// (e): the claim succeeds, the link race fires, but the refetch by
// subject errors, so the failure wraps as "refetch after link race".
func TestClaimAnonymousSessionPlayer_LinkRaceRefetchErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{
		claimPlayer:  &Player{ID: 7, Email: "claimed@example.test"},
		linkErr:      ErrIdentityAlreadyLinked,
		bySubjectErr: errors.New("refetch failed"),
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "claimed@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "refetch after link race"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestClaimAnonymousSessionPlayer_GenericClaimErrorWraps pins branch
// (f): a claim error that is not ErrPlayerNotFound wraps as "claim
// anonymous player for oauth".
func TestClaimAnonymousSessionPlayer_GenericClaimErrorWraps(t *testing.T) {
	t.Parallel()

	store := &identityStubStore{claimErr: errors.New("claim blew up")}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), store, 7, "sub", "x@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "claim anonymous player for oauth"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}
