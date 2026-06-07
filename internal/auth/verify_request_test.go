package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestHandleVerifyEmailRequestForm_AnonymousRenders(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	rec := runVerifyRequestGET(t, stores.Players)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="email"`; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

func TestHandleVerifyEmailRequestForm_SignedInRedirectsToLanding(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	p, err := stores.Players.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	sessions := session.New([]byte("k"), true)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email/request", nil)
	rec := httptest.NewRecorder()
	sessions.Set(rec, p.ID, p.SessionVersion)
	req.AddCookie(extractSessionCookie(rec))
	rec = httptest.NewRecorder()

	csrfMgr := csrf.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, VerifyRequestFlashCookieName, VerifyRequestFlashCookiePath)
	HandleVerifyEmailRequestForm(discardLogger(), csrfMgr, stores.Players, sessions, flash).ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestHandleVerifyEmailRequestSubmit_AlwaysFlashesGenericSuccess pins
// the account-existence-opaque contract: identical 303 + Location for
// every input shape (blank, malformed, unknown, real-verified,
// real-unverified).
func TestHandleVerifyEmailRequestSubmit_AlwaysFlashesGenericSuccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		email string
	}{
		{name: "blank email", email: ""},
		{name: "malformed email", email: "not-an-email"},
		{name: "unknown email", email: "ghost@example.test"},
		{name: "real verified email", email: "verified@example.test"},
		{name: "real unverified email", email: "unverified@example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stores := store.New(dbtest.Open(t), discardLogger())
			verifiedPlayer, err := stores.Players.CreatePlayer(
				t.Context(), "verified", "verified@example.test", "h", RolePlayer,
			)
			if err != nil {
				t.Fatalf("CreatePlayer verified err = %v, want nil", err)
			}
			markVerifiedViaStore(t, stores.VerifyTokens, verifiedPlayer.ID)
			if _, err := stores.Players.CreatePlayer(
				t.Context(), "unverified", "unverified@example.test", "h", RolePlayer,
			); err != nil {
				t.Fatalf("CreatePlayer unverified err = %v, want nil", err)
			}

			rec := runVerifyRequestPOST(t, stores.Players, &recordingVerifyTokenStore{}, &recordingSender{},
				NewVerifyResendLimiter(time.Minute, nil), tc.email)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := rec.Header().Get("Location"), "/verify-email/request"; got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}
		})
	}
}

func TestHandleVerifyEmailRequestSubmit_UnverifiedMatchDispatchesEmail(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	if _, err := stores.Players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}

	rec := runVerifyRequestPOST(t, stores.Players, tokens, sender,
		NewVerifyResendLimiter(time.Minute, nil), "alice@example.test")
	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	waitFor(t, func() bool {
		return len(sender.Sent()) >= 1 && len(tokens.Created()) >= 1
	})

	sent := sender.Sent()
	if got, want := len(sent), 1; got != want {
		t.Fatalf("sender.Sent() len = %d, want %d", got, want)
	}
	if got, want := sent[0].To, "alice@example.test"; got != want {
		t.Errorf("recipient = %q, want %q", got, want)
	}
}

func TestHandleVerifyEmailRequestSubmit_AlreadyVerifiedSendsNoMail(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	p, err := stores.Players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerifiedViaStore(t, stores.VerifyTokens, p.ID)
	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}

	runVerifyRequestPOST(t, stores.Players, tokens, sender,
		NewVerifyResendLimiter(time.Minute, nil), "alice@example.test")

	time.Sleep(50 * time.Millisecond)
	if got, want := len(sender.Sent()), 0; got != want {
		t.Errorf("sender.Sent() len = %d, want %d (already-verified must not dispatch)", got, want)
	}
}

func TestHandleVerifyEmailRequestSubmit_UnknownEmailSendsNoMail(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}

	runVerifyRequestPOST(t, stores.Players, tokens, sender,
		NewVerifyResendLimiter(time.Minute, nil), "ghost@example.test")

	time.Sleep(50 * time.Millisecond)
	if got, want := len(sender.Sent()), 0; got != want {
		t.Errorf("sender.Sent() len = %d, want %d (unknown email must not dispatch)", got, want)
	}
}

func TestHandleVerifyEmailRequestSubmit_RateLimitedBlocksDispatch(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	if _, err := stores.Players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}
	limiter := NewVerifyResendLimiter(time.Minute, nil)

	first := runVerifyRequestPOST(t, stores.Players, tokens, sender, limiter, "alice@example.test")
	if got, want := first.Code, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}
	second := runVerifyRequestPOST(t, stores.Players, tokens, sender, limiter, "alice@example.test")
	if got, want := second.Code, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
}

// markVerifiedViaStore stamps email_verified_at on the player by minting
// then consuming a verify token through the real store - the same path
// production uses to flip the column. The PlayerStore interface exposes
// no direct verify toggle, so the token round-trip is how a test marks a
// real row verified.
func markVerifiedViaStore(t *testing.T, tokens VerifyTokenStore, playerID int64) {
	t.Helper()

	raw, hash, err := GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if err := tokens.CreateVerifyToken(t.Context(), hash, playerID, time.Now().Add(time.Hour), ""); err != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", err)
	}
	if _, err := tokens.ConsumeVerifyToken(t.Context(), HashVerifyToken(raw)); err != nil {
		t.Fatalf("ConsumeVerifyToken err = %v, want nil", err)
	}
}

func runVerifyRequestGET(t *testing.T, players PlayerStore) *httptest.ResponseRecorder {
	t.Helper()
	csrfMgr := csrf.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, VerifyRequestFlashCookieName, VerifyRequestFlashCookiePath)
	sessions := session.New([]byte("k"), true)
	handler := HandleVerifyEmailRequestForm(discardLogger(), csrfMgr, players, sessions, flash)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email/request", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func runVerifyRequestPOST(
	t *testing.T,
	players PlayerStore,
	tokens VerifyTokenStore,
	sender VerifyEmailSender,
	limiter *VerifyResendLimiter,
	email string,
) *httptest.ResponseRecorder {
	t.Helper()
	sessions := session.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, VerifyRequestFlashCookieName, VerifyRequestFlashCookiePath)
	handler := HandleVerifyEmailRequestSubmit(
		discardLogger(), players, sessions,
		VerifyRequestDispatchDeps{Tokens: tokens, Sender: sender, BaseURL: "https://topbanana.example"},
		limiter, flash,
	)

	form := url.Values{"email": {email}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/verify-email/request",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}
