//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestVerifyGate_UnverifiedAdminBouncesToPending pins #111 PR3:
// registering through /register leaves email_verified_at NULL, so the
// very next request to /admin must 303 to /verify-email/pending instead
// of rendering the admin dashboard. The first password-bearing register
// becomes admin (per the existing rule in CreatePlayerWithCredentials),
// so a single registration covers the admin branch of the gate.
func TestVerifyGate_UnverifiedAdminBouncesToPending(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	loc := registerForRedirect(ctx, t, client, srv.BaseURL, "gate-admin", "gate-admin-pass-123")
	if got, want := loc, "/admin/quizzes"; got != want {
		t.Fatalf("register Location = %q, want %q", got, want)
	}

	resp := getWith(ctx, t, client, srv.BaseURL+"/admin/quizzes")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/verify-email/pending"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestVerifyGate_PendingPageShowsRecipientEmail pins the interstitial
// render: an unverified signed-in visitor sees their email rendered in
// the body so they can tell which address the link will go to.
func TestVerifyGate_PendingPageShowsRecipientEmail(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	registerForRedirect(ctx, t, client, srv.BaseURL, "gate-pending", "gate-pending-pass-123")

	resp := getWith(ctx, t, client, srv.BaseURL+"/verify-email/pending")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "gate-pending@example.test"; !strings.Contains(got, want) {
		t.Errorf("body missing recipient email %q", want)
	}
	if got, want := string(body), "Resend verification email"; !strings.Contains(got, want) {
		t.Errorf("body missing resend button copy %q", want)
	}
}

// TestVerifyGate_PendingPageWithoutSessionRedirectsToLogin pins that
// the interstitial is itself an authenticated page; reaching it
// anonymously redirects to /login with the next-path back to the
// interstitial. Otherwise an anonymous visitor could pin the resend
// form open without ever having registered.
func TestVerifyGate_PendingPageWithoutSessionRedirectsToLogin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	resp := getWith(ctx, t, authClient(t), srv.BaseURL+"/verify-email/pending")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/login?next=%2Fverify-email%2Fpending"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestVerifyGate_AfterVerifyAdminReachesDashboard pins the happy-path
// roundtrip: register → stamp email_verified_at via the verify-email
// flow → admin dashboard becomes reachable. Bypasses the email channel
// by minting the token directly through the store, the same trick
// verify_email_test.go uses.
func TestVerifyGate_AfterVerifyAdminReachesDashboard(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	registerForRedirect(ctx, t, client, srv.BaseURL, "gate-after", "gate-after-pass-123")

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByUsername(ctx, "gate-after")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if err := stores.VerifyTokens.CreateVerifyToken(ctx, hash, player.ID, futureHour(), ""); err != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", err)
	}

	verify := getWith(ctx, t, client, srv.BaseURL+"/verify-email?"+url.Values{"token": {raw}}.Encode())
	verify.Body.Close() //nolint:errcheck // cleanup.
	if got, want := verify.StatusCode, http.StatusOK; got != want {
		t.Fatalf("verify status = %d, want %d", got, want)
	}

	resp := getWith(ctx, t, client, srv.BaseURL+"/admin/quizzes")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("post-verify /admin/quizzes status = %d, want %d", got, want)
	}
}

// TestVerifyGate_ResendRateLimited pins the resend rate limiter: two
// consecutive POSTs from the same IP within the cool-down render the
// "Slow down" flash on the second attempt, not double-dispatch.
func TestVerifyGate_ResendRateLimited(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	registerForRedirect(ctx, t, client, srv.BaseURL, "gate-resend", "gate-resend-pass-123")
	csrfToken := fetchCSRFToken(ctx, t, client, srv.BaseURL+"/verify-email/pending")

	first := postResend(ctx, t, client, srv.BaseURL, csrfToken)
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first resend status = %d, want %d", got, want)
	}

	second := postResend(ctx, t, client, srv.BaseURL, csrfToken)
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second resend status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty, want non-empty on rate-limited resend")
	}

	follow := getWith(ctx, t, client, srv.BaseURL+"/verify-email/pending")
	defer follow.Body.Close() //nolint:errcheck // cleanup.
	body, err := io.ReadAll(follow.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Slow down"; !strings.Contains(got, want) {
		t.Errorf("rate-limited body missing %q", want)
	}
}

// getWith issues a GET with the supplied client + URL, asserting the
// request constructs cleanly. Centralised so each subtest stays short.
func getWith(ctx context.Context, t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}

// postResend issues POST /verify-email/resend with a freshly-fetched
// CSRF token and the client's existing cookie jar. Returns the raw
// response so the caller can assert status / headers.
func postResend(ctx context.Context, t *testing.T, client *http.Client, baseURL, csrfToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/verify-email/resend", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}

// futureHour returns a moment one hour in the future, in UTC, so the
// store-side expiry comparison stays lexicographically sane (see the
// notes on ConsumeEmailVerifyToken in players.sql).
func futureHour() time.Time { return time.Now().Add(time.Hour).UTC() }
