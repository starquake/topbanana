package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/store"
)

// collidingIdentityStore is a fault-injection OAuthIdentityStore that
// forces CreatePlayerFromOAuth to report a display-name collision a
// fixed number of times before succeeding. The real PlayerStore cannot
// express this: petnames are generated randomly, so a genuine collision
// on N consecutive create attempts is not reproducible on demand. These
// two tests pin the bounded petname-retry loop in createGooglePlayer,
// which only fires on the ErrDisplayNameTaken sentinel, so the
// collision must be injected. Every other method returns a not-found /
// unsupported result so an accidental call on a path these tests do not
// exercise surfaces loudly rather than passing silently.
type collidingIdentityStore struct {
	// createColl is the number of remaining forced collisions. Each
	// CreatePlayerFromOAuth call decrements it and returns
	// ErrDisplayNameTaken until it reaches zero, after which a player is
	// returned normally.
	createColl int
	nextID     int64
}

func (*collidingIdentityStore) GetPlayerByProviderSubject(
	_ context.Context, _, _ string,
) (*Player, error) {
	return nil, ErrPlayerNotFound
}

func (*collidingIdentityStore) GetPlayerByEmail(_ context.Context, _ string) (*Player, error) {
	return nil, ErrPlayerNotFound
}

func (s *collidingIdentityStore) CreatePlayerFromOAuth(
	_ context.Context, displayName, email string,
) (*Player, error) {
	if s.createColl > 0 {
		s.createColl--

		return nil, ErrDisplayNameTaken
	}
	s.nextID++

	return &Player{
		ID:          s.nextID,
		DisplayName: displayName,
		Email:       email,
		Role:        RolePlayer,
	}, nil
}

func (*collidingIdentityStore) LinkProviderIdentity(
	_ context.Context, _ int64, _, _ string,
) error {
	return nil
}

func (*collidingIdentityStore) ClaimPlayerForOAuth(
	_ context.Context, _ int64, _ string,
) (*Player, error) {
	return nil, errors.ErrUnsupported
}

func (*collidingIdentityStore) MarkPlayerEmailVerifiedIfNew(_ context.Context, _ int64) error {
	return errors.ErrUnsupported
}

// TestLinkOrCreateGooglePlayer_NewPlayer covers the most common path:
// no existing identity, no existing email, so a fresh row is created
// via CreatePlayerFromOAuth and the identity row is linked onto it.
func TestLinkOrCreateGooglePlayer_NewPlayer(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	player, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-1", "fresh@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.Email, "fresh@example.test"; got != want {
		t.Errorf("player.Email = %q, want %q", got, want)
	}
	if player.Role == "" {
		t.Errorf("player.Role = %q, want non-empty", player.Role)
	}

	// A second call with the same subject reads the existing identity
	// row and returns the same player.
	again, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-1", "fresh@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("second ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := again.ID, player.ID; got != want {
		t.Errorf("second call player.ID = %d, want %d (same row)", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_RetriesPetnameCollision pins the
// bounded retry on a CreatePlayerFromOAuth collision: the loop
// re-rolls a petname and tries again, and only gives up after a small
// number of attempts. Driven by collidingIdentityStore because a real
// store cannot force a petname collision on demand.
func TestLinkOrCreateGooglePlayer_RetriesPetnameCollision(t *testing.T) {
	t.Parallel()

	fake := &collidingIdentityStore{createColl: 2}

	player, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), fake, "google-sub-retry", "retry@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if player == nil {
		t.Fatal("ExportLinkOrCreateGooglePlayer returned nil player after retries")
	}
	if got, want := fake.createColl, 0; got != want {
		t.Errorf("collision counter = %d, want %d (all retries consumed)", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_ExhaustsRetries ensures the loop does
// not spin forever when collisions keep firing; the caller sees a
// wrapped ErrDisplayNameTaken instead. Driven by collidingIdentityStore
// because a real store cannot force a petname collision on demand.
func TestLinkOrCreateGooglePlayer_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	fake := &collidingIdentityStore{createColl: 100} // far more than the loop allows

	_, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), fake, "google-sub-exhaust", "exhaust@example.test", nil, true,
	)
	if err == nil {
		t.Fatal("ExportLinkOrCreateGooglePlayer err = nil, want non-nil after exhausting retries")
	}
	if got, want := err, ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want it to wrap %v", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_ExistingIdentity_StampsVerifiedEmail
// pins the self-heal at the existing-identity short-circuit: when an
// earlier link path linked the identity row but a transient failure
// prevented the email_verified_at stamp, every subsequent Google login
// otherwise short-circuits via GetPlayerByProviderSubject without
// retrying the stamp, stranding the player on /verify-email/pending.
// The handler now stamps idempotently on this branch when the row's
// email_verified_at is still nil. See #471.
func TestLinkOrCreateGooglePlayer_ExistingIdentity_StampsVerifiedEmail(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// CreatePlayer writes email but leaves email_verified_at NULL, so the
	// linked-but-unstamped state the heal branch repairs is reproduced
	// without going through the OAuth path.
	stranded, err := players.CreatePlayer(
		t.Context(), "stranded", "stranded@example.test", "h", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if err = players.LinkProviderIdentity(
		t.Context(), stranded.ID, ProviderGoogle, "google-sub-heal",
	); err != nil {
		t.Fatalf("seed LinkProviderIdentity err = %v, want nil", err)
	}
	if stranded.EmailVerifiedAt != nil {
		t.Fatal("seed must leave EmailVerifiedAt nil to exercise the heal branch")
	}

	healed, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-heal", "stranded@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := healed.ID, stranded.ID; got != want {
		t.Errorf("player.ID = %d, want %d (same row, not a new one)", got, want)
	}
	if healed.EmailVerifiedAt == nil {
		t.Error("EmailVerifiedAt = nil, want non-nil (stamp should fire on heal)")
	}
}

// TestLinkOrCreateGooglePlayer_ExistingIdentity_AlreadyVerifiedNoop
// pins the no-op path: a row that is already verified must not be
// re-stamped or otherwise mutated by the heal branch.
func TestLinkOrCreateGooglePlayer_ExistingIdentity_AlreadyVerifiedNoop(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// CreatePlayerFromOAuth stamps email_verified_at, giving an
	// already-verified row the heal branch must leave untouched.
	verified, err := players.CreatePlayerFromOAuth(
		t.Context(), "verified", "verified@example.test",
	)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if verified.EmailVerifiedAt == nil {
		t.Fatal("seed must have EmailVerifiedAt set to exercise the no-op branch")
	}
	verifiedAt := *verified.EmailVerifiedAt
	if err = players.LinkProviderIdentity(
		t.Context(), verified.ID, ProviderGoogle, "google-sub-verified",
	); err != nil {
		t.Fatalf("seed LinkProviderIdentity err = %v, want nil", err)
	}

	got, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-verified", "verified@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(verifiedAt) {
		t.Errorf("EmailVerifiedAt = %v, want preserved %v", got.EmailVerifiedAt, verifiedAt)
	}
}

// TestLinkOrCreateGooglePlayer_LinkExistingEmail pins the silent
// account-linking rule: a Google sign-in whose verified email matches
// an existing players.email attaches the identity to that row and
// returns the existing player.
func TestLinkOrCreateGooglePlayer_LinkExistingEmail(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	existing, err := players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	player, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-2", "alice@example.test", nil, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.ID, existing.ID; got != want {
		t.Errorf("player.ID = %d, want %d (linked, not created)", got, want)
	}
	if got, want := player.DisplayName, "alice"; got != want {
		t.Errorf("player.DisplayName = %q, want %q", got, want)
	}

	// Lookup by subject now resolves to the same row.
	bySubject, err := players.GetPlayerByProviderSubject(t.Context(), ProviderGoogle, "google-sub-2")
	if err != nil {
		t.Fatalf("GetPlayerByProviderSubject err = %v, want nil", err)
	}
	if got, want := bySubject.ID, existing.ID; got != want {
		t.Errorf("bySubject.ID = %d, want %d", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_ClaimsAnonymousSession pins the
// session-claim path: when the request already has a session pointing
// at a fully anonymous players row, the OAuth callback upgrades that
// row in place instead of creating a new one. The visitor keeps their
// player_id (and any custom displayName) on first Google sign-in.
func TestLinkOrCreateGooglePlayer_ClaimsAnonymousSession(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	anon, err := players.CreateAnonymousPlayer(t.Context(), "happy-banana")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	player, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-claim", "claim@example.test", &anon.ID, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.ID, anon.ID; got != want {
		t.Errorf("player.ID = %d, want %d (anonymous row reused, not replaced)", got, want)
	}
	if got, want := player.DisplayName, "happy-banana"; got != want {
		t.Errorf("player.DisplayName = %q, want %q (preserved across claim)", got, want)
	}
	if got, want := player.Email, "claim@example.test"; got != want {
		t.Errorf("player.Email = %q, want %q (set on claim)", got, want)
	}

	// A subsequent sign-in with the same subject resolves through the
	// identity lookup and lands on the same row.
	bySubject, err := players.GetPlayerByProviderSubject(t.Context(), ProviderGoogle, "google-sub-claim")
	if err != nil {
		t.Fatalf("GetPlayerByProviderSubject err = %v, want nil", err)
	}
	if got, want := bySubject.ID, anon.ID; got != want {
		t.Errorf("bySubject.ID = %d, want %d", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_SessionWithNonAnonymousRowFallsThrough
// pins the safety guard: a session pointing at a row that is no
// longer anonymous (e.g. password-registered in another tab, or
// previously OAuth-linked) skips the claim and falls through to the
// create-fresh-player path. The stale-session row is left untouched.
func TestLinkOrCreateGooglePlayer_SessionWithNonAnonymousRowFallsThrough(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	credentialled, err := players.CreatePlayer(
		t.Context(), "settled", "settled@example.test", "h", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	player, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-fallthrough", "newcomer@example.test", &credentialled.ID, true,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.ID, credentialled.ID; got == want {
		t.Errorf("player.ID = %d, must differ from stale-session row id %d", got, want)
	}
	if got, want := player.Email, "newcomer@example.test"; got != want {
		t.Errorf("player.Email = %q, want %q", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_RegistrationDisabled_RefusesNewAccount
// pins the registration gate: with registration off, a brand-new
// Google user (no existing identity, no session) is refused with
// ErrRegistrationDisabled rather than minting a fresh account.
func TestLinkOrCreateGooglePlayer_RegistrationDisabled_RefusesNewAccount(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	_, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-newcomer", "newcomer@example.test", nil, false,
	)
	if got, want := err, ErrRegistrationDisabled; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_RegistrationDisabled_ExistingIdentityLogsIn
// pins that an existing-identity sign-in still succeeds with
// registration off - only the create-fresh branch is gated.
func TestLinkOrCreateGooglePlayer_RegistrationDisabled_ExistingIdentityLogsIn(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	existing, err := players.CreatePlayerFromOAuth(
		t.Context(), "existing", "existing@example.test",
	)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if err = players.LinkProviderIdentity(
		t.Context(), existing.ID, ProviderGoogle, "google-sub-existing",
	); err != nil {
		t.Fatalf("seed LinkProviderIdentity err = %v, want nil", err)
	}

	got, err := ExportLinkOrCreateGooglePlayer(
		t.Context(), players, "google-sub-existing", "existing@example.test", nil, false,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := got.ID, existing.ID; got != want {
		t.Errorf("player.ID = %d, want %d (existing row)", got, want)
	}
}

// TestClaimAnonymousSessionPlayer_RecoversFromConcurrentLink pins the
// race-recovery branch in claimAnonymousSessionPlayer: when
// ClaimPlayerForOAuth returns ErrPlayerNotFound (because a concurrent
// callback for the same session already credentialled the row), the
// code re-reads by (provider, subject) and returns the already-linked
// player instead of falling through to create a duplicate. The
// post-race state is reproduced directly: the session row already has
// an email (so the claim guard fails) AND the identity is already
// linked, mirroring what the loser of a real concurrent race observes.
func TestClaimAnonymousSessionPlayer_RecoversFromConcurrentLink(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// The winning callback's row: already credentialled with an email,
	// so ClaimPlayerForOAuth's anonymous-only guard rejects this caller.
	winner, err := players.CreatePlayerFromOAuth(
		t.Context(), "winner", "winner@example.test",
	)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	// Pre-link the identity to the winning row so the recovery lookup
	// finds it.
	if err = players.LinkProviderIdentity(
		t.Context(), winner.ID, ProviderGoogle, "google-sub-race",
	); err != nil {
		t.Fatalf("seed LinkProviderIdentity err = %v, want nil", err)
	}

	// The loser passes its own session player id (also pointing at
	// winner.ID because both callbacks share the cookie) and the same
	// email + subject the winner used.
	got, err := ExportClaimAnonymousSessionPlayer(
		t.Context(), players, winner.ID, "google-sub-race", "winner@example.test",
	)
	if err != nil {
		t.Fatalf("ExportClaimAnonymousSessionPlayer err = %v, want nil", err)
	}
	if got == nil || got.ID != winner.ID {
		t.Errorf("recovered player.ID = %v, want %d (the winner's row)", got, winner.ID)
	}
}

// TestClaimAnonymousSessionPlayer_NoRaceFallsThrough pins the
// no-race path: a failed claim with no concurrent linker still
// returns ErrPlayerNotFound so the caller falls through to
// createGooglePlayer.
func TestClaimAnonymousSessionPlayer_NoRaceFallsThrough(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// Row exists but is no longer claimable (email already set);
	// nothing has linked the subject yet.
	row, err := players.CreatePlayerFromOAuth(
		t.Context(), "stale", "stale@example.test",
	)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}

	got, err := ExportClaimAnonymousSessionPlayer(
		t.Context(), players, row.ID, "google-sub-unlinked", "stale@example.test",
	)
	if err == nil {
		t.Fatalf("err = nil, want ErrPlayerNotFound (got player=%v)", got)
	}
	if !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("err = %v, want it to wrap ErrPlayerNotFound", err)
	}
}

// TestCreateGooglePlayer_RecoversFromConcurrentLink pins the
// symmetric race-recovery branch in createGooglePlayer: a concurrent
// callback for the same (provider, subject) linked the identity onto
// another row between our identity-lookup miss and our
// LinkProviderIdentity call. The code returns the already-linked row
// instead of erroring out.
func TestCreateGooglePlayer_RecoversFromConcurrentLink(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	winner, err := players.CreatePlayerFromOAuth(
		t.Context(), "racewinner", "racewinner@example.test",
	)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
	}
	if err = players.LinkProviderIdentity(
		t.Context(),
		winner.ID,
		ProviderGoogle,
		"google-sub-create-race",
	); err != nil {
		t.Fatalf("seed LinkProviderIdentity err = %v, want nil", err)
	}

	got, err := ExportCreateGooglePlayer(
		t.Context(), players, "google-sub-create-race", "newcomer@example.test",
	)
	if err != nil {
		t.Fatalf("ExportCreateGooglePlayer err = %v, want nil", err)
	}
	if got == nil || got.ID != winner.ID {
		t.Errorf("recovered player.ID = %v, want %d (the winner's row)", got, winner.ID)
	}
	// The orphan row that createGooglePlayer's CreatePlayerFromOAuth
	// inserted before the link failure is observable but harmless
	// (nothing links to it). The test does not assert its absence -
	// future cleanup is the operator's call.
}

// TestSignAndValidateState_RoundTrip pins the state-cookie HMAC: a
// signed value validates against the same key and fails against any
// other.
func TestSignAndValidateState_RoundTrip(t *testing.T) {
	t.Parallel()

	const nonce = "deterministic-nonce-for-testing"
	key := []byte("session-key-for-tests")

	signed := ExportSignState(key, nonce)
	if signed == "" {
		t.Fatal("signState returned empty string")
	}

	// Same key, same nonce in the cookie, same value in the query =>
	// valid.
	if err := ExportVerifySignedState(key, signed, signed); err != nil {
		t.Errorf("validateState(matching) err = %v, want nil", err)
	}

	// Different key => invalid signature.
	if err := ExportVerifySignedState(
		[]byte("other"),
		signed,
		signed,
	); !errors.Is(
		err,
		ErrGoogleStateMismatch,
	) {
		t.Errorf("validateState(other key) err = %v, want %v", err, ErrGoogleStateMismatch)
	}

	// Mismatched cookie vs query => invalid.
	tampered := signed[:len(signed)-1] + "x"
	if err := ExportVerifySignedState(key, signed, tampered); !errors.Is(err, ErrGoogleStateMismatch) {
		t.Errorf("validateState(mismatch) err = %v, want %v", err, ErrGoogleStateMismatch)
	}
}

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

	identities := &identityStubStore{byEmailErr: errors.New("db down")}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), identities, "sub", "x@example.test")
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
	identities := &identityStubStore{
		byEmailPlayer:   &Player{ID: 7, DisplayName: "alice", Email: "alice@example.test"},
		linkErr:         ErrIdentityAlreadyLinked,
		bySubjectPlayer: winner,
	}

	got, err := ExportLinkExistingPlayerByEmail(t.Context(), identities, "sub", "alice@example.test")
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

	identities := &identityStubStore{
		byEmailPlayer: &Player{ID: 7, Email: "alice@example.test"},
		linkErr:       ErrIdentityAlreadyLinked,
		bySubjectErr:  errors.New("refetch failed"),
	}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), identities, "sub", "alice@example.test")
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

	identities := &identityStubStore{
		byEmailPlayer:   &Player{ID: 7, Email: "alice@example.test"},
		markVerifiedErr: errors.New("stamp failed"),
	}

	_, err := ExportLinkExistingPlayerByEmail(t.Context(), identities, "sub", "alice@example.test")
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

	identities := &identityStubStore{byEmailPlayer: &Player{ID: 7, Email: "alice@example.test"}}

	got, err := ExportLinkExistingPlayerByEmail(t.Context(), identities, "sub", "alice@example.test")
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
	identities := &identityStubStore{
		claimErr:        ErrPlayerNotFound,
		bySubjectPlayer: winner,
	}

	got, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "x@example.test")
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

	identities := &identityStubStore{
		claimErr:     ErrPlayerNotFound,
		bySubjectErr: errors.New("lookup blew up"),
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "x@example.test")
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

	identities := &identityStubStore{
		claimErr:     ErrPlayerNotFound,
		bySubjectErr: ErrPlayerNotFound,
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "x@example.test")
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
	identities := &identityStubStore{
		claimPlayer:     &Player{ID: 7, Email: "claimed@example.test"},
		linkErr:         ErrIdentityAlreadyLinked,
		bySubjectPlayer: winner,
	}

	got, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "claimed@example.test")
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

	identities := &identityStubStore{
		claimPlayer:  &Player{ID: 7, Email: "claimed@example.test"},
		linkErr:      ErrIdentityAlreadyLinked,
		bySubjectErr: errors.New("refetch failed"),
	}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "claimed@example.test")
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

	identities := &identityStubStore{claimErr: errors.New("claim blew up")}

	_, err := ExportClaimAnonymousSessionPlayer(t.Context(), identities, 7, "sub", "x@example.test")
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got, want := err.Error(), "claim anonymous player for oauth"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// TestGoogleAuthenticator_ConcurrentInitRetry pins #622: when OIDC
// discovery keeps failing, concurrent requests retry initialisation
// without a data race. Driven through the exported HandleGoogleLogin
// (which calls the unexported ensureProvider) against an unreachable
// issuer so every request takes the discovery-failure retry path. Run
// under -race this fails against the old sync.Once-reset retry (which
// reassigned the Once and read/wrote initErr unlocked, and could even
// deadlock) and passes with the mutex-guarded version.
func TestGoogleAuthenticator_ConcurrentInitRetry(t *testing.T) {
	t.Parallel()

	authn := NewGoogleAuthenticator(GoogleConfig{
		ClientID:  "test-client",
		IssuerURL: "http://127.0.0.1:1", // connection refused -> fast discovery failure
	}, []byte("test-session-key-0123456789abcdef"))
	handler := HandleGoogleLogin(discardLogger(), authn)

	ctx := t.Context()
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/login/google", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got, want := rec.Code, http.StatusInternalServerError; got != want {
				t.Errorf("HandleGoogleLogin status = %d, want %d for an unreachable issuer", got, want)
			}
		}()
	}
	wg.Wait()
}
