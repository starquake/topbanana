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
)

// TestVerifyEmailRequest_GETRendersForm pins that an anonymous visitor
// reaches the public verify-request form and sees the email input + a
// CSRF token.
func TestVerifyEmailRequest_GETRendersForm(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	resp := getWith(ctx, t, authClient(t), srv.BaseURL+"/verify-email/request")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	for _, want := range []string{`name="email"`, `name="csrf_token"`} {
		if got := string(body); !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestVerifyEmailRequest_POSTAlwaysFlashesGenericSuccess pins the
// account-existence-opaque contract: identical response for blank,
// malformed, unknown, real-verified, and real-unverified addresses.
// Each subtest gets its own server so the per-IP rate limiter does not
// turn the second POST into a "slow down" flash.
func TestVerifyEmailRequest_POSTAlwaysFlashesGenericSuccess(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		email string
	}{
		{name: "blank", email: ""},
		{name: "malformed", email: "not-an-email"},
		{name: "unknown", email: "ghost@example.test"},
		{name: "verified", email: "verify-req-verified@example.test"},
		{name: "unverified", email: "verify-req-unverified@example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, srv := startServer(t, map[string]string{
				"REGISTRATION_ENABLED": "true",
			})
			dbConn, stores := openStores(t, srv.DBURI)
			defer dbConn.Close() //nolint:errcheck // cleanup.

			if _, err := stores.Players.CreatePlayer(
				ctx, "verify-req-verified", "verify-req-verified@example.test", "h", "player",
			); err != nil {
				t.Fatalf("CreatePlayer verified err = %v, want nil", err)
			}
			if _, err := stores.Players.CreatePlayer(
				ctx, "verify-req-unverified", "verify-req-unverified@example.test", "h", "player",
			); err != nil {
				t.Fatalf("CreatePlayer unverified err = %v, want nil", err)
			}
			verifyPlayerEmail(ctx, t, srv.DBURI, "verify-req-verified")

			client := authClient(t)
			resp := postVerifyRequest(ctx, t, client, srv.BaseURL, tc.email)
			defer resp.Body.Close() //nolint:errcheck // cleanup.
			if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := resp.Header.Get("Location"), "/verify-email/request"; got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}

			follow := getWith(ctx, t, client, srv.BaseURL+"/verify-email/request")
			defer follow.Body.Close() //nolint:errcheck // cleanup.
			body, err := io.ReadAll(follow.Body)
			if err != nil {
				t.Fatalf("ReadAll err = %v, want nil", err)
			}
			if got, want := string(body), "If an account matches"; !strings.Contains(got, want) {
				t.Errorf("body missing generic flash; body=%q", got)
			}
		})
	}
}

// TestVerifyEmailRequest_RateLimited pins that two POSTs from one IP in
// quick succession get rate-limited on the second, with Retry-After
// surfaced as a non-zero integer.
func TestVerifyEmailRequest_RateLimited(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	first := postVerifyRequest(ctx, t, client, srv.BaseURL, "anyone@example.test")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := postVerifyRequest(ctx, t, client, srv.BaseURL, "anyone@example.test")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
}

// TestVerifyEmailRequest_UnverifiedMatchMintsToken pins the only
// side-effect the opacity-preserving handler is allowed to have: a
// real, unverified row triggers an email_verify_tokens row being
// persisted. The integration server's mailer is the no-op stub, so
// the token row is what the test asserts against; SendVerifyEmail
// commits the token before the no-op Send returns ErrNotConfigured.
func TestVerifyEmailRequest_UnverifiedMatchMintsToken(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// BASE_URL must be set or SendVerifyEmail short-circuits in
		// buildVerifyLink before committing the token row, which would
		// make the assertion below fail for the wrong reason.
		"BASE_URL": "https://topbanana.example",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-req-real", "verify-req-real@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	client := authClient(t)
	resp := postVerifyRequest(ctx, t, client, srv.BaseURL, "verify-req-real@example.test")
	resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	waitForVerifyTokenRow(ctx, t, dbConn, player.ID)
}

// TestVerifyEmailRequest_AlreadyVerifiedMintsNoToken pins the silent
// no-op branch: a real but already-verified row must not produce a
// verify-token row, even though the user-facing flash is identical to
// the success branch.
func TestVerifyEmailRequest_AlreadyVerifiedMintsNoToken(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-req-done", "verify-req-done@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	verifyPlayerEmail(ctx, t, srv.DBURI, "verify-req-done")

	client := authClient(t)
	resp := postVerifyRequest(ctx, t, client, srv.BaseURL, "verify-req-done@example.test")
	resp.Body.Close() //nolint:errcheck // cleanup.

	// 200ms is long enough that an async dispatch would have committed
	// its token row by now; if the assertion is going to fail, this is
	// the wall-clock window in which it does.
	time.Sleep(200 * time.Millisecond)
	if got, want := countVerifyTokens(ctx, t, dbConn, player.ID), 0; got != want {
		t.Errorf("email_verify_tokens rows for already-verified player = %d, want %d", got, want)
	}
}

// postVerifyRequest fetches the CSRF token from /verify-email/request,
// then POSTs the form with the given email. Returns the raw response
// so the caller can assert status / headers.
func postVerifyRequest(ctx context.Context, t *testing.T, client *http.Client, baseURL, email string) *http.Response {
	t.Helper()
	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/verify-email/request")

	form := url.Values{}
	form.Add("email", email)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/verify-email/request", strings.NewReader(form.Encode()),
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

// waitForVerifyTokenRow polls until at least one email_verify_tokens
// row exists for playerID, or the test's deadline expires. The async
// dispatch goroutine commits the row before SMTP send, so polling on
// the row is the lightweight signal we need.
func waitForVerifyTokenRow(ctx context.Context, t *testing.T, dbConn *sql.DB, playerID int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if countVerifyTokens(ctx, t, dbConn, playerID) >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no email_verify_tokens row appeared for player_id=%d within deadline", playerID)
}

// countVerifyTokens returns the live row count for the given player.
func countVerifyTokens(ctx context.Context, t *testing.T, dbConn *sql.DB, playerID int64) int {
	t.Helper()
	var n int
	if err := dbConn.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM email_verify_tokens WHERE player_id = ?`, playerID,
	).Scan(&n); err != nil {
		t.Fatalf("count email_verify_tokens err = %v, want nil", err)
	}

	return n
}
