package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

func TestHandleResetForm_LivePreflightRendersForm(t *testing.T) {
	t.Parallel()

	tokens := &lookupResetStore{live: true, playerID: 42}
	rec := runResetForm(t, tokens, "raw-token")

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="token"`; !strings.Contains(got, want) {
		t.Errorf("body should render form token field %q", want)
	}
	if got, want := tokens.lookedUpHash, HashResetToken("raw-token"); got != want {
		t.Errorf("lookedUpHash = %q, want %q", got, want)
	}
}

func TestHandleResetForm_EmptyTokenRendersInvalid(t *testing.T) {
	t.Parallel()

	tokens := &lookupResetStore{}
	rec := runResetForm(t, tokens, "")

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if tokens.lookedUpHash != "" {
		t.Errorf("LookupResetToken called for empty token; lookedUpHash = %q", tokens.lookedUpHash)
	}
}

func TestHandleResetForm_DeadTokenRendersInvalid(t *testing.T) {
	t.Parallel()

	tokens := &lookupResetStore{live: false}
	rec := runResetForm(t, tokens, "consumed-or-expired")

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Link is no longer valid"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

func TestHandleResetForm_LookupErrorFallsOpen(t *testing.T) {
	t.Parallel()

	tokens := &lookupResetStore{lookupErr: errors.New("transient db failure")}
	rec := runResetForm(t, tokens, "raw-token")

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d (preflight error must fall open)", got, want)
	}
}

func TestHandleResetSubmit_AutoLoginToRoleLanding(t *testing.T) {
	t.Parallel()

	players := newStubPlayerStore()
	admin, err := players.CreatePlayer(t.Context(), "reset-admin", "reset-admin@example.test", "old-hash", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	admin.SessionVersion = 7

	tokens := &consumeResetStore{playerID: admin.ID}
	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, tokens, sessions, players)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	id, version, ok := decodeSessionCookie(t, sessions, rec)
	if !ok {
		t.Fatal("reset response did not set a valid session cookie")
	}
	if got, want := id, admin.ID; got != want {
		t.Errorf("session player id = %d, want %d", got, want)
	}
	// The cookie must carry the post-rotation version (read back from
	// the store), not a stale one, or the next request would reject it.
	if got, want := version, admin.SessionVersion; got != want {
		t.Errorf("session version = %d, want %d (post-rotation value)", got, want)
	}
}

func TestHandleResetSubmit_PlayerLandsOnHome(t *testing.T) {
	t.Parallel()

	players := newStubPlayerStore()
	p, err := players.CreatePlayer(t.Context(), "reset-player", "reset-player@example.test", "old-hash", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	// The first password-bearing registrant is auto-promoted to admin;
	// force the role back to player so this test exercises the home landing.
	p.Role = RolePlayer

	tokens := &consumeResetStore{playerID: p.ID}
	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, tokens, sessions, players)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if _, _, ok := decodeSessionCookie(t, sessions, rec); !ok {
		t.Error("reset response did not set a valid session cookie")
	}
}

// TestHandleResetSubmit_LookupFailureFallsBackToLogin pins the
// graceful-degradation path: the password change already committed, so
// a failed auto-login lookup must clear the session and 303 to /login
// rather than 500 in a way that hides the successful reset.
func TestHandleResetSubmit_LookupFailureFallsBackToLogin(t *testing.T) {
	t.Parallel()

	players := newStubPlayerStore()
	players.failGet = true

	tokens := &consumeResetStore{playerID: 99}
	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, tokens, sessions, players)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	// Fallback clears the session: the cookie is set with an empty value.
	if _, _, ok := decodeSessionCookie(t, sessions, rec); ok {
		t.Error("fallback must not leave a live session cookie")
	}
}

func TestHandleResetSubmit_InvalidTokenRendersGone(t *testing.T) {
	t.Parallel()

	tokens := &consumeResetStore{consumeErr: ErrResetTokenInvalid}
	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, tokens, sessions, newStubPlayerStore())

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func runResetSubmit(
	t *testing.T,
	tokens ResetTokenStore,
	sessions *session.Manager,
	players PlayerStore,
) *httptest.ResponseRecorder {
	t.Helper()

	handler := HandleResetSubmit(discardLogger(), nil, tokens, sessions, players)
	form := url.Values{}
	form.Set("token", "raw-token")
	form.Set("password", "new-pass-12345")
	form.Set("confirm", "new-pass-12345")
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/reset-password", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// decodeSessionCookie replays the Set-Cookie from rec onto a fresh
// request and asks the manager to decode it, so the test verifies the
// cookie the handler minted is actually valid (not just present).
func decodeSessionCookie(
	t *testing.T, sessions *session.Manager, rec *httptest.ResponseRecorder,
) (playerID, sessionVersion int64, ok bool) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}

	return sessions.Decode(req)
}

// consumeResetStore is a ResetTokenStore whose ConsumeResetToken
// returns a fixed player id (or a fixed error). The lookup/create/
// delete surface is uninteresting for the POST path and returns
// errors.ErrUnsupported.
type consumeResetStore struct {
	playerID   int64
	consumeErr error
}

func (s *consumeResetStore) ConsumeResetToken(_ context.Context, _, _ string) (int64, error) {
	if s.consumeErr != nil {
		return 0, s.consumeErr
	}

	return s.playerID, nil
}

func (*consumeResetStore) LookupResetToken(_ context.Context, _ string) (int64, bool, error) {
	return 0, false, errors.ErrUnsupported
}

func (*consumeResetStore) CreateResetToken(_ context.Context, _ string, _ int64, _ time.Time) error {
	return errors.ErrUnsupported
}

func (*consumeResetStore) DeleteExpiredResetTokens(_ context.Context) error {
	return nil
}

var _ ResetTokenStore = (*consumeResetStore)(nil)

func runResetForm(t *testing.T, tokens ResetTokenStore, raw string) *httptest.ResponseRecorder {
	t.Helper()

	handler := HandleResetForm(discardLogger(), nil, tokens)
	target := "/reset-password"
	if raw != "" {
		target += "?token=" + raw
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// lookupResetStore is a ResetTokenStore that records the
// LookupResetToken call so the test can pin the hash the handler
// passed; ConsumeResetToken and the rest of the surface are
// uninteresting for the GET-form path and return errors.ErrUnsupported.
type lookupResetStore struct {
	live         bool
	playerID     int64
	lookupErr    error
	lookedUpHash string
}

func (s *lookupResetStore) LookupResetToken(_ context.Context, tokenHash string) (int64, bool, error) {
	s.lookedUpHash = tokenHash
	if s.lookupErr != nil {
		return 0, false, s.lookupErr
	}

	return s.playerID, s.live, nil
}

func (*lookupResetStore) CreateResetToken(_ context.Context, _ string, _ int64, _ time.Time) error {
	return errors.ErrUnsupported
}

func (*lookupResetStore) ConsumeResetToken(_ context.Context, _, _ string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (*lookupResetStore) DeleteExpiredResetTokens(_ context.Context) error {
	return nil
}

var _ ResetTokenStore = (*lookupResetStore)(nil)
