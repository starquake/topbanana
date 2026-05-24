package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// stubOAuthStore is an in-memory OAuthIdentityStore for unit tests
// targeting the find-or-link decision in linkOrCreateGooglePlayer (via
// its exported test alias). It does not implement the credentialled
// PlayerStore methods because the OAuth flow does not touch them.
type stubOAuthStore struct {
	mu             sync.Mutex
	players        map[int64]*auth.Player
	byEmail        map[string]*auth.Player
	identities     map[identityKey]int64
	nextID         int64
	createErr      error
	createColl     int
	failGetEmail   bool
	failGetSubject bool
	failLink       bool
}

type identityKey struct {
	Provider string
	Subject  string
}

func newStubOAuthStore() *stubOAuthStore {
	return &stubOAuthStore{
		players:    map[int64]*auth.Player{},
		byEmail:    map[string]*auth.Player{},
		identities: map[identityKey]int64{},
		nextID:     1,
	}
}

func (s *stubOAuthStore) GetPlayerByProviderSubject(_ context.Context, provider, subject string) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failGetSubject {
		return nil, errors.New("boom")
	}
	id, ok := s.identities[identityKey{Provider: provider, Subject: subject}]
	if !ok {
		return nil, auth.ErrPlayerNotFound
	}

	return s.players[id], nil
}

func (s *stubOAuthStore) GetPlayerByEmail(_ context.Context, email string) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failGetEmail {
		return nil, errors.New("boom")
	}
	p, ok := s.byEmail[email]
	if !ok {
		return nil, auth.ErrPlayerNotFound
	}

	return p, nil
}

func (s *stubOAuthStore) CreatePlayerFromOAuth(_ context.Context, username, email string) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createColl > 0 {
		s.createColl--

		return nil, auth.ErrUsernameTaken
	}
	if _, exists := s.byEmail[email]; exists && email != "" {
		return nil, auth.ErrUsernameTaken
	}

	p := &auth.Player{
		ID:       s.nextID,
		Username: username,
		Email:    email,
		Role:     auth.RolePlayer,
	}
	s.nextID++
	s.players[p.ID] = p
	if email != "" {
		s.byEmail[email] = p
	}

	return p, nil
}

func (s *stubOAuthStore) LinkProviderIdentity(_ context.Context, playerID int64, provider, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failLink {
		return errors.New("boom")
	}
	key := identityKey{Provider: provider, Subject: subject}
	if _, exists := s.identities[key]; exists {
		return auth.ErrIdentityAlreadyLinked
	}
	s.identities[key] = playerID

	return nil
}

func (s *stubOAuthStore) ClaimPlayerForOAuth(_ context.Context, playerID int64, email string) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.players[playerID]
	if !ok {
		return nil, auth.ErrPlayerNotFound
	}
	// Mirror the SQL's "anonymous only" guard so the stub fails the
	// same way the production query would when the row has already
	// been credentialled or carries an email.
	if p.PasswordHash != "" || p.Email != "" {
		return nil, auth.ErrPlayerNotFound
	}
	p.Email = email
	if email != "" {
		s.byEmail[email] = p
	}

	return p, nil
}

// seedAnonymous inserts a fully anonymous players row (no password,
// no email) so the session-claim test has a target the
// ClaimPlayerForOAuth guard accepts.
func (s *stubOAuthStore) seedAnonymous(username string) *auth.Player {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := &auth.Player{
		ID:       s.nextID,
		Username: username,
		Role:     auth.RolePlayer,
	}
	s.nextID++
	s.players[p.ID] = p

	return p
}

// seed inserts a player row directly so the linking test has an
// existing target without going through CreatePlayerFromOAuth.
func (s *stubOAuthStore) seed(email, username, role string) *auth.Player {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := &auth.Player{
		ID:       s.nextID,
		Username: username,
		Email:    email,
		Role:     role,
	}
	s.nextID++
	s.players[p.ID] = p
	if email != "" {
		s.byEmail[email] = p
	}

	return p
}

// TestLinkOrCreateGooglePlayer_NewPlayer covers the most common path:
// no existing identity, no existing email, so a fresh row is created
// and the identity row is linked onto it.
func TestLinkOrCreateGooglePlayer_NewPlayer(t *testing.T) {
	t.Parallel()

	store := newStubOAuthStore()
	player, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-1", "fresh@example.test", nil,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.Email, "fresh@example.test"; got != want {
		t.Errorf("player.Email = %q, want %q", got, want)
	}
	// The SQL CASE that promotes the first password-less registrant to
	// admin is exercised by the integration test against a real DB;
	// this stub returns a plain "player" by default. Asserting only on
	// presence keeps the unit test focused on the find-or-link
	// decision tree, not on the SQL promotion rule.
	if player.Role == "" {
		t.Errorf("player.Role = %q, want non-empty", player.Role)
	}

	// A second call with the same subject reads the existing identity
	// row and returns the same player.
	again, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-1", "fresh@example.test", nil,
	)
	if err != nil {
		t.Fatalf("second ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := again.ID, player.ID; got != want {
		t.Errorf("second call player.ID = %d, want %d (same row)", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_LinkExistingEmail pins the silent
// account-linking rule: a Google sign-in whose verified email matches
// an existing players.email attaches the identity to that row and
// returns the existing player.
func TestLinkOrCreateGooglePlayer_LinkExistingEmail(t *testing.T) {
	t.Parallel()

	store := newStubOAuthStore()
	existing := store.seed("alice@example.test", "alice", auth.RolePlayer)

	player, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-2", "alice@example.test", nil,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.ID, existing.ID; got != want {
		t.Errorf("player.ID = %d, want %d (linked, not created)", got, want)
	}
	if got, want := player.Username, "alice"; got != want {
		t.Errorf("player.Username = %q, want %q", got, want)
	}

	// Lookup by subject now resolves to the same row.
	bySubject, err := store.GetPlayerByProviderSubject(t.Context(), auth.ProviderGoogle, "google-sub-2")
	if err != nil {
		t.Fatalf("GetPlayerByProviderSubject err = %v, want nil", err)
	}
	if got, want := bySubject.ID, existing.ID; got != want {
		t.Errorf("bySubject.ID = %d, want %d", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_RetriesPetnameCollision pins the
// bounded retry on a CreatePlayerFromOAuth collision: the loop
// re-rolls a petname and tries again, and only gives up after a small
// number of attempts.
func TestLinkOrCreateGooglePlayer_RetriesPetnameCollision(t *testing.T) {
	t.Parallel()

	store := newStubOAuthStore()
	store.createColl = 2

	player, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-retry", "retry@example.test", nil,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if player == nil {
		t.Fatal("ExportLinkOrCreateGooglePlayer returned nil player after retries")
	}
	if got, want := store.createColl, 0; got != want {
		t.Errorf("collision counter = %d, want %d (all retries consumed)", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_ExhaustsRetries ensures the loop does
// not spin forever when collisions keep firing; the caller sees a
// wrapped ErrUsernameTaken instead.
func TestLinkOrCreateGooglePlayer_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	store := newStubOAuthStore()
	store.createColl = 100 // far more than the loop allows

	_, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-exhaust", "exhaust@example.test", nil,
	)
	if err == nil {
		t.Fatal("ExportLinkOrCreateGooglePlayer err = nil, want non-nil after exhausting retries")
	}
	if got, want := err, auth.ErrUsernameTaken; !errors.Is(got, want) {
		t.Errorf("err = %v, want it to wrap %v", got, want)
	}
}

// TestLinkOrCreateGooglePlayer_ClaimsAnonymousSession pins the
// session-claim path: when the request already has a session pointing
// at a fully anonymous players row, the OAuth callback upgrades that
// row in place instead of creating a new one. The visitor keeps their
// player_id (and any custom username) on first Google sign-in.
func TestLinkOrCreateGooglePlayer_ClaimsAnonymousSession(t *testing.T) {
	t.Parallel()

	store := newStubOAuthStore()
	anon := store.seedAnonymous("happy-banana")

	player, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-claim", "claim@example.test", &anon.ID,
	)
	if err != nil {
		t.Fatalf("ExportLinkOrCreateGooglePlayer err = %v, want nil", err)
	}
	if got, want := player.ID, anon.ID; got != want {
		t.Errorf("player.ID = %d, want %d (anonymous row reused, not replaced)", got, want)
	}
	if got, want := player.Username, "happy-banana"; got != want {
		t.Errorf("player.Username = %q, want %q (preserved across claim)", got, want)
	}
	if got, want := player.Email, "claim@example.test"; got != want {
		t.Errorf("player.Email = %q, want %q (set on claim)", got, want)
	}

	// A subsequent sign-in with the same subject resolves through the
	// identity lookup and lands on the same row.
	bySubject, err := store.GetPlayerByProviderSubject(t.Context(), auth.ProviderGoogle, "google-sub-claim")
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

	store := newStubOAuthStore()
	credentialled := store.seed("settled@example.test", "settled", auth.RolePlayer)

	player, err := auth.ExportLinkOrCreateGooglePlayer(
		t.Context(), store, "google-sub-fallthrough", "newcomer@example.test", &credentialled.ID,
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

// TestSignAndValidateState_RoundTrip pins the state-cookie HMAC: a
// signed value validates against the same key and fails against any
// other.
func TestSignAndValidateState_RoundTrip(t *testing.T) {
	t.Parallel()

	const nonce = "deterministic-nonce-for-testing"
	key := []byte("session-key-for-tests")

	signed := auth.ExportSignState(key, nonce)
	if signed == "" {
		t.Fatal("signState returned empty string")
	}

	// Same key, same nonce in the cookie, same value in the query =>
	// valid.
	if err := auth.ExportVerifySignedState(key, signed, signed); err != nil {
		t.Errorf("validateState(matching) err = %v, want nil", err)
	}

	// Different key => invalid signature.
	if err := auth.ExportVerifySignedState(
		[]byte("other"),
		signed,
		signed,
	); !errors.Is(
		err,
		auth.ErrGoogleStateMismatch,
	) {
		t.Errorf("validateState(other key) err = %v, want %v", err, auth.ErrGoogleStateMismatch)
	}

	// Mismatched cookie vs query => invalid.
	tampered := signed[:len(signed)-1] + "x"
	if err := auth.ExportVerifySignedState(key, signed, tampered); !errors.Is(err, auth.ErrGoogleStateMismatch) {
		t.Errorf("validateState(mismatch) err = %v, want %v", err, auth.ErrGoogleStateMismatch)
	}
}
