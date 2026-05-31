//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/store"
)

// TestVerifyEmail_RoundtripHappyPath covers the end-to-end happy path:
// generate + persist a token via the store, hit GET /verify-email with
// the raw token, observe the 200 + flashes-no-banner response, and
// confirm the player's email_verified_at column got stamped.
//
// The HTTP register path is exercised in admin/auth tests already - this
// test goes through the store directly so it can recover the raw token
// the email link carries, which the hash-only DB column cannot reveal.
func TestVerifyEmail_RoundtripHappyPath(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // test cleanup, error path is uninteresting.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-happy", "verify-happy@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if got := player.EmailVerifiedAt; got != nil {
		t.Errorf("EmailVerifiedAt = %v, want nil before verify", got)
	}

	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(ctx, hash, player.ID, time.Now().Add(time.Hour), ""); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	resp := getVerifyEmail(ctx, t, srv.BaseURL, raw)
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got := refreshed.EmailVerifiedAt; got == nil {
		t.Error("EmailVerifiedAt = nil after verify, want stamped")
	}
}

// TestVerifyEmail_DuplicateClickReadsAsAlreadyUsed pins the duplicate-click
// branch: a second click on a freshly used link must render the
// already-verified page, not an "invalid link" error, so a mail-client
// prefetch followed by a user click does not look like a failure.
func TestVerifyEmail_DuplicateClickReadsAsAlreadyUsed(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // test cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-dupe", "verify-dupe@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(ctx, hash, player.ID, time.Now().Add(time.Hour), ""); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	first := getVerifyEmail(ctx, t, srv.BaseURL, raw)
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusOK; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := getVerifyEmail(ctx, t, srv.BaseURL, raw)
	defer second.Body.Close() //nolint:errcheck // cleanup.
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := second.StatusCode, http.StatusOK; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got, want := string(body), "Already verified"; !strings.Contains(got, want) {
		t.Errorf("second body should contain %q", want)
	}
}

// TestVerifyEmail_InvalidToken hits the no-row branch: an unknown token
// hash returns 410 Gone with the "Link is no longer valid" page so the
// caller can tell apart "this email is verified" from "your link is
// junk" via the status code.
func TestVerifyEmail_InvalidToken(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	resp := getVerifyEmail(ctx, t, srv.BaseURL, "not-a-real-token")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestVerifyEmail_ExpiredToken hits the expired branch: a row whose
// expires_at is in the past must read as "Link is no longer valid"
// rather than letting the user complete verification on a stale link.
func TestVerifyEmail_ExpiredToken(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // test cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-expired", "verify-expired@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(
		ctx,
		hash,
		player.ID,
		time.Now().Add(-time.Hour),
		"",
	); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	resp := getVerifyEmail(ctx, t, srv.BaseURL, raw)
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got := refreshed.EmailVerifiedAt; got != nil {
		t.Errorf("EmailVerifiedAt = %v, want nil after expired-token consume", got)
	}
}

// TestVerifyEmail_MismatchedSessionClears pins the #472 shared-device
// fix: when the session cookie belongs to a different player than the
// token owner, GET /verify-email must clear the session and render a
// neutral landing target rather than send the operator to someone
// else's role landing.
func TestVerifyEmail_MismatchedSessionClears(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	// Register user A, verify, and sign them in so clientA holds a live
	// session cookie. The #574 hard gate means register alone no longer
	// produces one.
	clientA := authClient(t)
	registerVerifyAndSignIn(ctx, t, clientA, srv.BaseURL, srv.DBURI, "verify-session-a", "session-a-pass-123")

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	// Mint a token for user B (a different row).
	userB, err := stores.Players.CreatePlayer(
		ctx, "verify-token-b", "verify-token-b@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer userB err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(ctx, hash, userB.ID, time.Now().Add(time.Hour), ""); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	// Hit /verify-email with userA's session cookie attached.
	target := srv.BaseURL + "/verify-email?" + url.Values{"token": {raw}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := clientA.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := string(body), `href="/"`; !strings.Contains(got, want) {
		t.Errorf("body should render neutral landing %q, got %q", want, got)
	}

	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "topbanana_session" && c.MaxAge < 0 {
			cleared = true

			break
		}
	}
	if !cleared {
		t.Errorf("expected session cookie to be cleared on mismatch; cookies = %v", resp.Cookies())
	}
}

// TestSendVerifyEmail_RoundtripStoresAndSends covers SendVerifyEmail
// against the real store + the diagnostics Tester wrapper. The
// recorded send log must show one entry for the verify-email kind so
// future "Recent send log" coverage on the admin page sees the row.
func TestSendVerifyEmail_RoundtripStoresAndSends(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // test cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "verify-send", "verify-send@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	tester := mailer.NewTester(mailer.NewNoop())
	err = auth.SendVerifyEmail(
		ctx, stores.VerifyTokens, tester,
		"https://topbanana.example", player.Email, player.ID, time.Now(),
	)
	if got, want := err, mailer.ErrNotConfigured; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v (no-op mailer wraps to ErrNotConfigured)", got, want)
	}
	if got, want := tester.Count(), 1; got != want {
		t.Errorf("tester.Count() = %d, want %d", got, want)
	}
}

// openStores returns a fresh *sql.DB + store.Stores pointing at the
// test server's DB. Used by the verify-email tests that need direct
// store access to create + inspect the token + player rows.
func openStores(t *testing.T, dbURI string) (*sql.DB, *store.Stores) {
	t.Helper()
	dbConn, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}

	return dbConn, store.New(dbConn, slog.Default())
}

// getVerifyEmail issues a single GET /verify-email?token=<raw> with a
// fresh client (no cookies, no redirect following) so test cases stay
// independent.
func getVerifyEmail(ctx context.Context, t *testing.T, baseURL, raw string) *http.Response {
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
	target := baseURL + "/verify-email?" + url.Values{"token": {raw}}.Encode()
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
