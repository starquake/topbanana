//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestAuthRedirect_PerRole pins the #288 fix end-to-end: register and
// login send admins to /admin/quizzes (existing behaviour) and players
// to / (new). The pre-fix code sent everyone to /admin/quizzes, which
// bounced non-admins to the Access Denied page; this test would fail
// against that code path.
//
// Subtests share state by design — the first one (admin register)
// seeds the admin row that the others rely on for ordering ("first
// password-bearing registrant becomes admin"). They run sequentially
// against a single boot of the server, so do not call t.Parallel()
// inside the subtests.
//
//nolint:paralleltest,tparallel // subtests share state and must run sequentially.
func TestAuthRedirect_PerRole(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL

	t.Run("admin register lands on /admin/quizzes", func(t *testing.T) {
		client := authClient(t)
		location := registerForRedirect(ctx, t, client, baseURL, "redirect-admin", "redirect-admin-pass-123")
		if got, want := location, "/admin/quizzes"; got != want {
			t.Errorf("admin register Location = %q, want %q", got, want)
		}
	})

	t.Run("player register lands on /", func(t *testing.T) {
		client := authClient(t)
		location := registerForRedirect(ctx, t, client, baseURL, "redirect-player", "redirect-player-pass-123")
		if got, want := location, "/"; got != want {
			t.Errorf("player register Location = %q, want %q", got, want)
		}
	})

	t.Run("admin login lands on /admin/quizzes", func(t *testing.T) {
		client := authClient(t)
		location := loginForRedirect(ctx, t, client, baseURL, "redirect-admin", "redirect-admin-pass-123")
		if got, want := location, "/admin/quizzes"; got != want {
			t.Errorf("admin login Location = %q, want %q", got, want)
		}
	})

	// Sleep past the per-IP login cool-down (#494) so the next subtest's
	// POST is not 429'd. The two login subtests come from the same
	// localhost peer and so share the limiter bucket.
	time.Sleep(auth.LoginCooldown() + 100*time.Millisecond)

	t.Run("player login lands on /", func(t *testing.T) {
		client := authClient(t)
		location := loginForRedirect(ctx, t, client, baseURL, "redirect-player", "redirect-player-pass-123")
		if got, want := location, "/"; got != want {
			t.Errorf("player login Location = %q, want %q", got, want)
		}
	})
}

// authClient builds a fresh http.Client with a cookie jar and the
// don't-follow-redirects policy every auth test in this package uses.
func authClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}

	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// registerForRedirect POSTs /register and returns the Location header
// from the 303 response so the caller can assert on it directly.
func registerForRedirect(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username, password string,
) string {
	t.Helper()

	return submitAuthForm(ctx, t, client, baseURL, "/register", username, password)
}

// loginForRedirect POSTs /login and returns the Location header from
// the 303 response.
func loginForRedirect(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username, password string,
) string {
	t.Helper()

	return submitAuthForm(ctx, t, client, baseURL, "/login", username, password)
}

// submitAuthForm shares the GET-CSRF + POST-form dance between the
// register and login probes. Asserts the response is 303 (anything
// else means the auth flow itself failed, not the redirect rule).
func submitAuthForm(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, path, username, password string,
) string {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+path)

	form := url.Values{}
	form.Add("username", username)
	// Always send email and password_confirm so the same helper drives
	// both /login (which ignores those fields) and /register (which
	// requires email since #111 and a matching password_confirm).
	form.Add("email", username+"@example.test")
	form.Add("password", password)
	form.Add("password_confirm", password)
	form.Add("csrf_token", token)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+path, strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("%s POST status = %d, want %d", path, got, want)
	}

	return resp.Header.Get("Location")
}
