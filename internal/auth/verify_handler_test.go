package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	_ context.Context, _ string, _ int64, _ time.Time,
) error {
	return nil
}

func (s *stubVerifyTokens) ConsumeVerifyToken(_ context.Context, tokenHash string) (int64, error) {
	s.consumedHash = tokenHash
	if s.consumeErr != nil {
		return 0, s.consumeErr
	}

	return s.consumePlayerID, nil
}

func (*stubVerifyTokens) DeleteExpiredVerifyTokens(_ context.Context) error {
	return nil
}
