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
	"github.com/starquake/topbanana/internal/session"
)

func TestHandleForgotForm_AnonymousRenders(t *testing.T) {
	t.Parallel()

	rec := runForgotGET(t, newStubPlayerStore(), nil)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="identifier"`; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

func TestHandleForgotForm_SignedInRedirectsToLanding(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	p, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer)
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
	HandleForgotForm(discardLogger(), csrfMgr, store, sessions, flash).ServeHTTP(rec, req)

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

			store := newStubPlayerStore()
			if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer); err != nil {
				t.Fatalf("CreatePlayer err = %v, want nil", err)
			}

			rec := runForgotPOST(t, store, &recordingResetTokenStore{}, &recordingSender{},
				NewVerifyResendLimiter(time.Minute), tc.identifier)

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

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}

	rec := runForgotPOST(t, store, tokens, sender,
		NewVerifyResendLimiter(time.Minute), "alice")
	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	// The dispatch goroutine fires async; wait briefly for it to land.
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

func TestHandleForgotSubmit_UnknownAccountSendsNoMail(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}

	runForgotPOST(t, store, tokens, sender,
		NewVerifyResendLimiter(time.Minute), "ghost")

	// Wait long enough that an async dispatch would have landed.
	time.Sleep(50 * time.Millisecond)
	if got, want := len(sender.Sent()), 0; got != want {
		t.Errorf("sender.Sent() len = %d, want %d (unknown account must not dispatch)", got, want)
	}
}

func TestHandleForgotSubmit_RateLimitedBlocksDispatch(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	tokens := &recordingResetTokenStore{}
	sender := &recordingSender{}
	limiter := NewVerifyResendLimiter(time.Minute)

	first := runForgotPOST(t, store, tokens, sender, limiter, "alice")
	if got, want := first.Code, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}
	second := runForgotPOST(t, store, tokens, sender, limiter, "alice")
	if got, want := second.Code, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
}

func runForgotGET(t *testing.T, store PlayerStore, _ *SignedFlash) *httptest.ResponseRecorder {
	t.Helper()
	csrfMgr := csrf.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, ForgotFlashCookieName, ForgotFlashCookiePath)
	sessions := session.New([]byte("k"), true)
	handler := HandleForgotForm(discardLogger(), csrfMgr, store, sessions, flash)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/forgot-password", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func runForgotPOST(
	t *testing.T,
	store PlayerStore,
	tokens ResetTokenStore,
	sender VerifyEmailSender,
	limiter *VerifyResendLimiter,
	identifier string,
) *httptest.ResponseRecorder {
	t.Helper()
	sessions := session.New([]byte("k"), true)
	flash := NewSignedFlash([]byte("k"), true, ForgotFlashCookieName, ForgotFlashCookiePath)
	handler := HandleForgotSubmit(
		discardLogger(), store, sessions, tokens, sender,
		"https://topbanana.example", limiter, flash,
	)

	form := url.Values{"identifier": {identifier}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/forgot-password",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func extractSessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "topbanana_session") {
			return c
		}
	}

	return nil
}

func waitFor(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: predicate never became true")
}
