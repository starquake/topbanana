//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

// TestVerifyEmail_SeparateConnectionStampVisibleToNextRequest is the
// regression guard for #555. The e2e helper markEmailVerified (and its
// integration mirror verifyPlayerEmail) stamps email_verified_at over a
// SEPARATE database connection, and #555 worried the server's very next
// request might not see that write and would bounce a just-verified
// player to /verify-email/pending via RequireVerifiedEmail.
//
// It cannot: the stamp is committed before the request returns, and the
// gate re-reads the player from the DB on every request
// (RequireVerifiedEmail -> PlayerFromContext <- EnsurePlayer ->
// GetPlayerByID), so WAL guarantees the read txn after the commit sees
// it. This test pins that by stamping on a separate connection and
// IMMEDIATELY (no sleep) hitting a gated endpoint, asserting 200. Run it
// under -race -count=200 to stress the stamp-then-request window: zero
// bounces is the proof.
func TestVerifyEmail_SeparateConnectionStampVisibleToNextRequest(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "verify-race@example.test",
	})
	baseURL := srv.BaseURL

	// First registrant is promoted to Admin via ADMIN_EMAILS but left
	// UNVERIFIED on purpose: we want RequireVerifiedEmail to bounce them
	// until the separate-connection stamp lands.
	const username = "verify-race"
	client := registerClientUnverified(ctx, t, baseURL, username)

	gated := baseURL + "/admin/quizzes"

	// Sanity: the gate is live. An unverified Admin is bounced to the
	// pending interstitial (303 + Location) rather than reaching the page.
	assertBouncedToPending(ctx, t, client, gated)

	// Stamp email_verified_at over a SEPARATE connection.
	verifyPlayerEmail(ctx, t, srv.DBURI, username)

	// IMMEDIATELY hit the same gated endpoint. No sleep: this is the
	// visibility window #555 worried about. The stamp must be visible to
	// the server's next request, so the player is NOT bounced.
	after := httpGet(ctx, t, client, gated)
	defer closeBody(t, after.Body)
	if got, want := after.StatusCode, http.StatusOK; got != want {
		if loc := after.Header.Get("Location"); loc != "" {
			t.Fatalf("post-stamp status = %d, want %d (bounced to %q; "+
				"separate-connection stamp NOT visible, #555 race)", got, want, loc)
		}
		t.Fatalf("post-stamp status = %d, want %d", got, want)
	}
}

// assertBouncedToPending fails the test unless a GET of target redirects
// (303) to the verify-email pending interstitial. The client preserves
// the redirect response (CheckRedirect returns ErrUseLastResponse), so
// the 303 + Location are inspected directly.
func assertBouncedToPending(ctx context.Context, t *testing.T, client *http.Client, target string) {
	t.Helper()
	resp := httpGet(ctx, t, client, target)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("pre-stamp status = %d, want %d (gate not bouncing unverified player)", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/verify-email/pending"; got != want {
		t.Fatalf("pre-stamp Location = %q, want %q", got, want)
	}
}

// registerClientUnverified mirrors registerAdminClient but stops after
// the register POST, leaving email_verified_at NULL so the caller can
// drive RequireVerifiedEmail in its unverified state. Returns the
// cookie-jar client carrying the new session.
func registerClientUnverified(ctx context.Context, t *testing.T, baseURL, username string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")
	form := url.Values{
		"username":         {username},
		"email":            {username + "@example.test"},
		"password":         {"integration-pass-123"},
		"password_confirm": {"integration-pass-123"},
		"csrf_token":       {token},
	}
	req := newFormReq(ctx, t, baseURL+"/register", form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("register %q err = %v, want nil", username, err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register %q status = %d, want %d", username, got, want)
	}

	return client
}
