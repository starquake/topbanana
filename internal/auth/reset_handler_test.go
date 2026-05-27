package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
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
