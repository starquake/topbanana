package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

func TestHandleVerifyEmail_MissingToken(t *testing.T) {
	t.Parallel()

	rec := runVerifyEmail(t, &stubVerifyTokens{}, "")
	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleVerifyEmail_Success(t *testing.T) {
	t.Parallel()

	store := &stubVerifyTokens{consumePlayerID: 99}
	rec := runVerifyEmail(t, store, "raw-token")

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := store.consumedHash, HashVerifyToken("raw-token"); got != want {
		t.Errorf("consumedHash = %q, want %q", got, want)
	}
}

func TestHandleVerifyEmail_AlreadyUsed(t *testing.T) {
	t.Parallel()

	rec := runVerifyEmail(t, &stubVerifyTokens{consumeErr: ErrVerifyTokenAlreadyUsed}, "raw-token")
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleVerifyEmail_Invalid(t *testing.T) {
	t.Parallel()

	rec := runVerifyEmail(t, &stubVerifyTokens{consumeErr: ErrVerifyTokenInvalid}, "raw-token")
	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestHandleVerifyEmail_MismatchedSessionClears pins the shared-device
// fix: when the session player differs from the token owner, the
// success page must clear the session and render the neutral /
// landing rather than the session player's role landing (#472).
func TestHandleVerifyEmail_MismatchedSessionClears(t *testing.T) {
	t.Parallel()

	players := newStubPlayerStore()
	sessionPlayer, err := players.CreatePlayer(
		t.Context(), "session-user", "session@example.test", "h", "admin",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &stubVerifyTokens{consumePlayerID: sessionPlayer.ID + 1}
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, sessionPlayer.ID, sessionPlayer.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	handler := HandleVerifyEmail(discardLogger(), nil, tokens, players, sessions)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token=raw-token", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)

	if got, want := out.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := out.Body.String(), `href="/"`; !strings.Contains(got, want) {
		t.Errorf("body should render neutral landing %q, got %q", want, got)
	}
	cleared := false
	for _, c := range out.Result().Cookies() {
		if c.Name == "topbanana_session" && c.MaxAge < 0 {
			cleared = true

			break
		}
	}
	if !cleared {
		t.Errorf("expected session cookie to be cleared; cookies = %v", out.Result().Cookies())
	}
}

// TestHandleVerifyEmail_MatchingSessionKeepsLanding pins the
// other half of the mismatch fix: when the session player matches the
// token owner, the Continue link goes to the role landing (admin in
// this case) and the session is preserved.
func TestHandleVerifyEmail_MatchingSessionKeepsLanding(t *testing.T) {
	t.Parallel()

	players := newStubPlayerStore()
	player, err := players.CreatePlayer(
		t.Context(), "match-user", "match@example.test", "h", "admin",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &stubVerifyTokens{consumePlayerID: player.ID}
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, player.ID, player.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	handler := HandleVerifyEmail(discardLogger(), nil, tokens, players, sessions)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token=raw-token", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)

	if got, want := out.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := out.Body.String(), `href="/admin/quizzes"`; !strings.Contains(got, want) {
		t.Errorf("body should render admin landing %q, got %q", want, got)
	}
}

func runVerifyEmail(t *testing.T, tokens *stubVerifyTokens, raw string) *httptest.ResponseRecorder {
	t.Helper()

	players := newStubPlayerStore()
	sessions := session.New([]byte("k"), true)
	handler := HandleVerifyEmail(discardLogger(), nil, tokens, players, sessions)

	target := "/verify-email"
	if raw != "" {
		target += "?token=" + raw
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

type stubVerifyTokens struct {
	consumePlayerID int64
	consumeErr      error
	consumedHash    string
}

func (*stubVerifyTokens) CreateVerifyToken(
	_ context.Context, _ string, _ int64, _ time.Time, _ string,
) error {
	return nil
}

func (s *stubVerifyTokens) ConsumeVerifyToken(_ context.Context, tokenHash string) (int64, error) {
	s.consumedHash = tokenHash
	if s.consumeErr != nil {
		return s.consumePlayerID, s.consumeErr
	}

	return s.consumePlayerID, nil
}

func (*stubVerifyTokens) DeleteExpiredVerifyTokens(_ context.Context) error {
	return nil
}
