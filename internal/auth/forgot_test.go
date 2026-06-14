package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestHandleForgotForm_AnonymousRenders(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	rec := runForgotGET(t, stores.Players)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="identifier"`; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

func TestHandleForgotForm_SignedInRedirectsToLanding(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	p, err := stores.Players.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	sessions := session.New([]byte("k"), true)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/forgot-password", nil)
	rec := httptest.NewRecorder()
	sessions.Set(rec, p.ID, p.SessionVersion)
	req.AddCookie(extractSessionCookie(rec))
	rec = httptest.NewRecorder()

	csrfMgr := csrf.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, ForgotFlashCookieName, ForgotFlashCookiePath)
	HandleForgotForm(discardLogger(), csrfMgr, stores.Players, sessions, flash).ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleForgotSubmit_AlwaysFlashesGenericSuccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		identifier string
	}{
		{name: "blank identifier", identifier: ""},
		{name: "unknown account", identifier: "ghost"},
		{name: "real account", identifier: "alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stores := store.New(dbtest.Open(t), discardLogger())
			if _, err := stores.Players.CreatePlayer(
				t.Context(), "alice", "alice@example.test", "h", RolePlayer,
			); err != nil {
				t.Fatalf("CreatePlayer err = %v, want nil", err)
			}

			rec, _ := runForgotPOST(t, stores.Players, &recordingResetTokenStore{}, &recordingSender{},
				NewVerifyResendLimiter(time.Minute, nil), tc.identifier)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := rec.Header().Get("Location"), "/forgot-password"; got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}
		})
	}
}

func TestHandleForgotSubmit_RealMatchDispatchesEmail(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	if _, err := stores.Players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}

	rec, tracker := runForgotPOST(t, stores.Players, tokens, sender,
		NewVerifyResendLimiter(time.Minute, nil), "alice")
	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	if err := tracker.Wait(t.Context()); err != nil {
		t.Fatalf("tracker.Wait err = %v, want nil", err)
	}

	sent := sender.Sent()
	if got, want := len(sent), 1; got != want {
		t.Fatalf("sender.Sent() len = %d, want %d", got, want)
	}
	if got, want := sent[0].To, "alice@example.test"; got != want {
		t.Errorf("recipient = %q, want %q", got, want)
	}
}

func TestHandleForgotSubmit_UnknownAccountSendsNoMail(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}

	_, tracker := runForgotPOST(t, stores.Players, tokens, sender,
		NewVerifyResendLimiter(time.Minute, nil), "ghost")

	// Tracker counts every dispatched goroutine. An unknown identifier
	// must return before Tasks.Go, so Wait completes immediately and
	// any post-Wait send would be a real bug, not a scheduling race.
	if err := tracker.Wait(t.Context()); err != nil {
		t.Fatalf("tracker.Wait err = %v, want nil", err)
	}
	if got, want := len(sender.Sent()), 0; got != want {
		t.Errorf("sender.Sent() len = %d, want %d (unknown account must not dispatch)", got, want)
	}
}

func TestHandleForgotSubmit_RateLimitedBlocksDispatch(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	if _, err := stores.Players.CreatePlayer(
		t.Context(), "alice", "alice@example.test", "h", RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}
	limiter := NewVerifyResendLimiter(time.Minute, nil)

	first, _ := runForgotPOST(t, stores.Players, tokens, sender, limiter, "alice")
	if got, want := first.Code, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}
	second, _ := runForgotPOST(t, stores.Players, tokens, sender, limiter, "alice")
	if got, want := second.Code, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
}

func runForgotGET(t *testing.T, players PlayerStore) *httptest.ResponseRecorder {
	t.Helper()
	csrfMgr := csrf.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, ForgotFlashCookieName, ForgotFlashCookiePath)
	sessions := session.New([]byte("k"), true)
	handler := HandleForgotForm(discardLogger(), csrfMgr, players, sessions, flash)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/forgot-password", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func runForgotPOST(
	t *testing.T,
	players PlayerStore,
	tokens ResetTokenStore,
	sender VerifyEmailSender,
	limiter *VerifyResendLimiter,
	identifier string,
) (*httptest.ResponseRecorder, *bgtasks.Tracker) {
	t.Helper()
	sessions := session.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, ForgotFlashCookieName, ForgotFlashCookiePath)
	tracker := bgtasks.New()
	handler := HandleForgotSubmit(
		discardLogger(), players, sessions,
		ForgotDispatchDeps{Tokens: tokens, Sender: sender, BaseURL: "https://topbanana.example", Tasks: tracker},
		limiter, flash,
	)

	form := url.Values{"identifier": {identifier}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/forgot-password",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec, tracker
}

func extractSessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "topbanana_session") {
			return c
		}
	}

	return nil
}
