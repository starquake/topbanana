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

// TestLogin_UnverifiedEmail_BlocksAndResends: an unverified account is
// refused with the generic 401 (no session) end-to-end, and the resend
// still fires - observed via the second email_verify_tokens row the
// dispatch commits before SMTP (#492/#1171).
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
		displayName = "login-unverified"
		password    = "correctbattery-unv-13"
	)

	// Register through the real /register handler so the resulting row
	// looks exactly like a production unverified registration: no
	// email_verified_at stamp and one verify-token row.
	regClient := authClient(t)
	regCSRF := fetchCSRFToken(ctx, t, regClient, srv.BaseURL+"/register")
	registerResp := postRegister(ctx, t, regClient, srv.BaseURL, regCSRF, displayName, password)
	registerResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := registerResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
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
	resp := postLoginFormFull(ctx, t, loginClient, srv.BaseURL, loginCSRF, displayName+"@example.test", password)
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("login status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Invalid email or password."; !strings.Contains(got, want) {
		t.Errorf("body missing generic invalid-credentials banner; body=%.300q", got)
	}
	// The response must not confirm the password was correct (#787/#1171).
	for _, dontWant := range []string{"Check your email", "resent the link to", "finish signing in"} {
		if strings.Contains(string(body), dontWant) {
			t.Errorf("body leaks credential-correct confirmation %q; body=%.300q", dontWant, string(body))
		}
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
	ctx context.Context, t *testing.T, client *http.Client, baseURL, csrfToken, displayName, password string,
) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Add("display_name", displayName)
	form.Add("email", displayName+"@example.test")
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
