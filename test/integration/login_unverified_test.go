//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/session"
)

// TestLogin_UnverifiedEmail_BlocksAndResends pins #492: a credentialled
// account whose email_verified_at is NULL must be refused at /login.
// Drives the full handler chain so route wiring, CSRF, the login
// limiter, and the verify-resend dispatch all participate.
//
// The integration server keeps the no-op mailer in front of the
// diagnostics Tester, so the side-effect we can observe is the
// email_verify_tokens row the dispatch commits before SMTP. Register
// commits one such row; a successful login-time resend pushes the
// count up to two. We also assert that no session cookie is issued
// and that the response body carries the verify banner.
func TestLogin_UnverifiedEmail_BlocksAndResends(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// BASE_URL must resolve or SendVerifyEmail short-circuits in
		// buildVerifyLink before the token row commits, which would make
		// the post-login count assertion fail for the wrong reason.
		"BASE_URL": "https://topbanana.example",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	const (
		username = "login-unverified"
		password = "correctbattery-unv-13"
	)

	// Register through the real /register handler so the resulting row
	// looks exactly like a production unverified registration: no
	// email_verified_at stamp and one verify-token row.
	regClient := authClient(t)
	regCSRF := fetchCSRFToken(ctx, t, regClient, srv.BaseURL+"/register")
	registerResp := postRegister(ctx, t, regClient, srv.BaseURL, regCSRF, username, password)
	registerResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := registerResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}

	player, err := stores.Players.GetPlayerByUsername(ctx, username)
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if player.EmailVerifiedAt != nil {
		t.Fatalf("EmailVerifiedAt = %v, want nil before login", player.EmailVerifiedAt)
	}
	waitForVerifyTokenRow(ctx, t, dbConn, player.ID)
	if got, want := countVerifyTokens(ctx, t, dbConn, player.ID), 1; got != want {
		t.Fatalf("verify tokens after register = %d, want %d", got, want)
	}

	// Attempt /login with the same credentials from a fresh client (no
	// inherited cookie jar) so the assertion about a missing session
	// cookie is unambiguous.
	loginClient := authClient(t)
	loginCSRF := fetchCSRFToken(ctx, t, loginClient, srv.BaseURL+"/login")
	resp := postLoginFormFull(ctx, t, loginClient, srv.BaseURL, loginCSRF, username+"@example.test", password)
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("login status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "verify your email"; !strings.Contains(got, want) {
		t.Errorf("body missing verify banner; body=%.300q", got)
	}
	if got, want := string(body), username+"@example.test"; !strings.Contains(got, want) {
		t.Errorf("body missing recipient address; body=%.300q", got)
	}
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("session cookie set on unverified login: %+v", c)
		}
	}

	waitForVerifyTokenCount(ctx, t, dbConn, player.ID, 2)
}

// waitForVerifyTokenCount polls until the verify-token row count for
// playerID is at least want, or fails the test. Distinct from
// waitForVerifyTokenRow (which pins >= 1) because the login-resend
// case wants to assert the row that just landed on top of the
// register-time row.
func waitForVerifyTokenCount(ctx context.Context, t *testing.T, dbConn *sql.DB, playerID int64, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if countVerifyTokens(ctx, t, dbConn, playerID) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("verify tokens for player_id=%d did not reach %d within deadline (have %d)",
		playerID, want, countVerifyTokens(ctx, t, dbConn, playerID))
}

// postRegister POSTs /register with the given credentials + pre-fetched
// CSRF token and returns the raw response so callers assert on status,
// body, and cookies themselves. After #574 a successful register
// renders the confirmation page with 200 and no session cookie.
func postRegister(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, csrfToken, username, password string,
) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Add("username", username)
	form.Add("email", username+"@example.test")
	form.Add("password", password)
	form.Add("password_confirm", password)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/register", strings.NewReader(form.Encode()),
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

// postLoginFormFull is the /login analogue of postRegister: pre-fetched
// CSRF token, full form payload, raw response. Distinct from
// postLoginWithToken (login_limiter_test.go) only in name so each test
// file can keep its helpers self-contained without one importing the
// other.
func postLoginFormFull(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, csrfToken, email, password string,
) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Add("email", email)
	form.Add("password", password)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()),
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
