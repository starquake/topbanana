package auth_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/starquake/topbanana/internal/auth"
)

// fakePlayerStore is a fault-injection PlayerStore for the handful of
// middleware tests that pin error branches a real store cannot produce
// on demand. Three knobs drive those branches:
//
//   - failGet makes GetPlayerByID return a non-sentinel error, so the
//     "load existing session player failed" 500 path fires without
//     having to corrupt a real row.
//   - forceAnonCollisions makes the next N CreateAnonymousPlayer calls
//     return ErrDisplayNameTaken, exercising the bounded petname retry
//     loop and its xid fallback. A real store cannot collide on demand:
//     petnames are generated randomly, so N consecutive forced
//     collisions are not reproducible against live UNIQUE indexes.
//   - forceAnonErr makes the next CreateAnonymousPlayer return a chosen
//     non-collision error, exercising the EnsurePlayer 500 branch.
//
// Every other interface method is minimal (mint an id on create,
// not-found / unsupported otherwise): these tests only ever reach the
// three knobbed paths, so an unexpected call on any other method should
// surface loudly rather than pass silently. Tautological tests that
// just need a working player store use a real store.New instead.
type fakePlayerStore struct {
	mu     sync.Mutex
	nextID int64
	// failGet, when true, makes GetPlayerByID return a non-sentinel error.
	failGet bool
	// forceAnonCollisions, when > 0, makes the next N CreateAnonymousPlayer
	// calls return ErrDisplayNameTaken without inserting. Each call
	// decrements the counter; once it reaches zero the fake returns to its
	// normal id-minting behaviour.
	forceAnonCollisions int
	// forceAnonErr, when set, is returned by the next CreateAnonymousPlayer
	// call and then cleared, so a single request exercises one error branch
	// without leaking the failure into a follow-up call.
	forceAnonErr error
}

func newFakePlayerStore() *fakePlayerStore {
	return &fakePlayerStore{nextID: 1}
}

func (s *fakePlayerStore) GetPlayerByID(_ context.Context, id int64) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failGet {
		return nil, errors.New("boom")
	}

	return &Player{ID: id, Role: RolePlayer}, nil
}

func (s *fakePlayerStore) CreateAnonymousPlayer(_ context.Context, displayName string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.forceAnonErr != nil {
		err := s.forceAnonErr
		s.forceAnonErr = nil

		return nil, err
	}
	if s.forceAnonCollisions > 0 {
		s.forceAnonCollisions--

		return nil, ErrDisplayNameTaken
	}

	p := &Player{ID: s.nextID, DisplayName: displayName, Role: RolePlayer}
	s.nextID++

	return p, nil
}

// CreatePlayer mints a row so the failGet tests can seed a player and set
// a session cookie before flipping the knob; role is irrelevant because
// failGet short-circuits GetPlayerByID before any role check.
func (s *fakePlayerStore) CreatePlayer(
	_ context.Context, displayName, email, passwordHash, _ string,
) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := &Player{
		ID:           s.nextID,
		DisplayName:  displayName,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         RolePlayer,
	}
	s.nextID++

	return p, nil
}

func (*fakePlayerStore) GetPlayerByDisplayName(_ context.Context, _ string) (*Player, error) {
	return nil, ErrPlayerNotFound
}

func (*fakePlayerStore) GetPlayerByEmail(_ context.Context, _ string) (*Player, error) {
	return nil, ErrPlayerNotFound
}

func (*fakePlayerStore) ClaimPlayer(
	_ context.Context, _ int64, _, _, _, _ string,
) (*Player, error) {
	return nil, errors.ErrUnsupported
}

func (*fakePlayerStore) UpdatePlayerDisplayName(
	_ context.Context, _ int64, _ string,
) (*Player, error) {
	return nil, errors.ErrUnsupported
}

func (*fakePlayerStore) RenamePlayer(_ context.Context, _ int64, _ string) (*Player, error) {
	return nil, errors.ErrUnsupported
}

func (*fakePlayerStore) SetPlayerPasswordHash(_ context.Context, _, _ string) error {
	return errors.ErrUnsupported
}

func (*fakePlayerStore) ChangePlayerPassword(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// findCookie returns the first response cookie with the given name and a
// boolean reporting whether it was found. Used by the EnsurePlayer tests to
// assert that a fresh session cookie is set on the response.
func findCookie(rec *httptest.ResponseRecorder, name string) (*http.Cookie, bool) {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c, true
		}
	}

	return nil, false
}
