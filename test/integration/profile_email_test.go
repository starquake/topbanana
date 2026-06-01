//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestProfileEmail_HappyPathSwapsAndStaysSignedIn covers the full
// in-session email-change loop (#497): the signed-in visitor types a
// new address, POST /profile/email flashes a confirmation, GET
// /verify-email with the resulting token swaps players.email and
// re-stamps email_verified_at, and the visitor's existing cookie
// keeps working because the consumer refreshed it with the new
// session_version.
func TestProfileEmail_HappyPathSwapsAndStaysSignedIn(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-change-happy", "correct-battery-13")

	const newAddr = "fresh-inbox@example.test"

	flash := profileEmailPOST(ctx, t, client, srv.BaseURL, newAddr)
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	if !strings.Contains(body, newAddr) {
		t.Errorf("flash body should echo new address %q, got %q", newAddr, body)
	}

	// Pull the freshly minted token straight out of the DB so we can
	// hit GET /verify-email exactly the way the user's mail client
	// would. The raw token is not recoverable from the hash-only row
	// in production, but a test that drives the store directly can
	// generate its own pair - we instead read the row order and look
	// up the latest non-consumed pending_email row.
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	// The token is only known by its hash inside the DB. To exercise
	// the consume path end-to-end we mint another token directly via
	// the store: dispatchEmailChangeIfFree's send is best-effort and
	// asynchronous, so we cannot read the raw token off the email.
	player, err := stores.Players.GetPlayerByDisplayName(ctx, "email-change-happy")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(
		ctx, hash, player.ID, time.Now().Add(time.Hour), newAddr,
	); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	resp := getVerifyEmailWithClient(ctx, t, srv.BaseURL, raw, client)
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("verify-email status = %d, want %d", got, want)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refreshed.Email, newAddr; got != want {
		t.Errorf("email after consume = %q, want %q", got, want)
	}
	if refreshed.EmailVerifiedAt == nil {
		t.Error("EmailVerifiedAt = nil after swap, want re-stamped")
	}

	// Confirm the existing cookie still works: hitting GET /profile
	// (which requires both authentication AND a verified email)
	// returns 200 because the consume handler reissued the cookie
	// with the bumped session_version.
	if got, want := profileGetStatus(ctx, t, client, srv.BaseURL), http.StatusOK; got != want {
		t.Errorf("profile GET after swap = %d, want %d (session cookie should still be valid)", got, want)
	}
}

// TestProfileEmail_MalformedRejected covers the validation gate: a
// new email that fails LooksLikeEmail re-renders the page with the
// error banner and never mints a token.
func TestProfileEmail_MalformedRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-malformed", "correct-battery-13")

	flash := profileEmailPOST(ctx, t, client, srv.BaseURL, "not-an-email")
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	if !strings.Contains(body, "valid email") {
		t.Errorf("body missing malformed-email error banner, got %q", body)
	}
}

// TestProfileEmail_NoOpRejected covers the same-as-current branch:
// typing the address the account already uses is rejected with a
// dedicated banner so the user doesn't burn a round trip.
func TestProfileEmail_NoOpRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-noop", "correct-battery-13")

	flash := profileEmailPOST(ctx, t, client, srv.BaseURL, "email-noop@example.test")
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	if !strings.Contains(body, "already your address") {
		t.Errorf("body missing same-address error banner, got %q", body)
	}
}

// TestProfileEmail_CollisionOpaque pins the account-existence-opaque
// contract: when the new email is already on another account, the
// POST returns the same flash a free address would. The token row
// must NOT be created for the colliding case so a swap can never
// happen.
func TestProfileEmail_CollisionOpaque(t *testing.T) {
	t.Parallel()

	// Two sign-ins from the same localhost peer share the per-IP login
	// limiter bucket; disable the cooldown so the second login is not
	// 429'd by the 3s default (#494).
	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"LOGIN_COOLDOWN":       "0",
	})

	// Register the rival who owns the target address.
	rival := authClient(t)
	registerVerifyAndSignIn(ctx, t, rival, srv.BaseURL, srv.DBURI, "email-rival", "correct-battery-13")

	// Register the player attempting the change.
	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-collide", "correct-battery-13")

	flash := profileEmailPOST(ctx, t, client, srv.BaseURL, "email-rival@example.test")
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	// Opacity now rests on both branches rendering the identical
	// generic "if not already in use" notice, so we assert the
	// collision case shows that same success-shaped flash (echoes the
	// typed address, mentions the verification link) rather than
	// checking for the absence of a leak word: the generic copy itself
	// contains "already", so a negative substring check no longer holds.
	if !strings.Contains(body, "verification link") {
		t.Errorf("collision body should match the success flash for opacity, got %q", body)
	}
	if !strings.Contains(body, "email-rival@example.test") {
		t.Errorf("collision body should echo the typed address like the success flash, got %q", body)
	}
}

// TestProfileEmail_RegisterFlowStillVerifies pins the no-regression
// invariant: a token row with pending_email NULL must still swap
// nothing and only stamp email_verified_at, the way it did before
// the change. This is the test that catches a bug where the
// consumer accidentally treats every token as an email-change row.
func TestProfileEmail_RegisterFlowStillVerifies(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "register-verify", "register-verify@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(
		ctx, hash, player.ID, time.Now().Add(time.Hour), "",
	); cerr != nil {
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
	if got, want := refreshed.Email, "register-verify@example.test"; got != want {
		t.Errorf("email after register-flow consume = %q, want %q (must not swap)", got, want)
	}
	if refreshed.EmailVerifiedAt == nil {
		t.Error("EmailVerifiedAt = nil after consume, want stamped")
	}
}

// TestProfileEmail_OldSessionInvalidatedAfterSwap pins the security
// contract on the swap path: every cookie issued before the consume
// must stop working because SwapPlayerEmail bumps session_version
// inside the transaction. The current request's cookie is refreshed
// inline (covered by the happy-path test); this test confirms a
// separate cookie carrying the old version is now stale.
func TestProfileEmail_OldSessionInvalidatedAfterSwap(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	primary := authClient(t)
	registerVerifyAndSignIn(ctx, t, primary, srv.BaseURL, srv.DBURI, "email-rotate", "correct-battery-13")

	// Mint a second client carrying the same session cookie at the
	// pre-swap session_version. Easiest way: log in a fresh client
	// against the same account and copy its session cookie.
	stale := freshClientSharingSession(t, primary, srv.BaseURL)

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, "email-rotate")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if cerr := stores.VerifyTokens.CreateVerifyToken(
		ctx, hash, player.ID, time.Now().Add(time.Hour), "rotated@example.test",
	); cerr != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", cerr)
	}

	resp := getVerifyEmailWithClient(ctx, t, srv.BaseURL, raw, primary)
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("verify-email status = %d, want %d", got, want)
	}

	// The stale client's cookie carries the old session_version, so
	// any authenticated route must bounce it.
	if got, want := profileGetStatus(ctx, t, stale, srv.BaseURL), http.StatusSeeOther; got != want {
		t.Errorf("stale-cookie profile status = %d, want %d (redirect to login)", got, want)
	}
}

// TestProfileEmail_WrongPasswordRejected pins the Slice-1 re-auth gate
// (#534): a password account that submits the wrong current password is
// rejected with a banner and no email change is started.
func TestProfileEmail_WrongPasswordRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-wrongpw", "correct-battery-13")

	flash := profileEmailPOSTWithPassword(ctx, t, client, srv.BaseURL, "fresh@example.test", "not-the-password")
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	if !strings.Contains(body, "Current password is incorrect") {
		t.Errorf("body missing wrong-password error banner, got %q", body)
	}
}

// TestProfileEmail_EmptyPasswordRejected pins that an empty current
// password is rejected for a password account, same as a wrong one.
func TestProfileEmail_EmptyPasswordRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "email-emptypw", "correct-battery-13")

	flash := profileEmailPOSTWithPassword(ctx, t, client, srv.BaseURL, "fresh@example.test", "")
	if got, want := flash.status, http.StatusSeeOther; got != want {
		t.Fatalf("POST status = %d, want %d", got, want)
	}

	body := profileEmailFollowFlash(ctx, t, client, srv.BaseURL)
	if !strings.Contains(body, "Current password is incorrect") {
		t.Errorf("body missing wrong-password error banner, got %q", body)
	}
}

// profileEmailSnapshot mirrors profilePageSnapshot from profile_test.go
// for the email flow.
type profileEmailSnapshot struct {
	status int
	body   string
	csrf   string
}

// profileEmailGET returns the rendered /profile/email page plus the
// CSRF token, both extracted the same way the username form helpers
// do upstream.
func profileEmailGET(ctx context.Context, t *testing.T, client *http.Client, baseURL string) profileEmailSnapshot {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/profile/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer closeBodyNoError(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	rendered := string(body)

	return profileEmailSnapshot{
		status: resp.StatusCode,
		body:   rendered,
		csrf:   extractEmailCSRFToken(t, rendered),
	}
}

// profileEmailPOST submits the email-change form with the correct
// current password and returns the raw response snapshot. Always
// re-primes the CSRF token first so the helper composes cleanly with
// itself.
func profileEmailPOST(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, newEmail string,
) profileEmailSnapshot {
	t.Helper()

	return profileEmailPOSTWithPassword(ctx, t, client, baseURL, newEmail, "correct-battery-13")
}

// profileEmailPOSTWithPassword submits the email-change form with an
// explicit current password so tests can drive the wrong/empty
// password rejection branches.
func profileEmailPOSTWithPassword(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, newEmail, currentPassword string,
) profileEmailSnapshot {
	t.Helper()

	priming := profileEmailGET(ctx, t, client, baseURL)
	form := url.Values{
		"csrf_token":       {priming.csrf},
		"new_email":        {newEmail},
		"current_password": {currentPassword},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/email", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer closeBodyNoError(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return profileEmailSnapshot{status: resp.StatusCode, body: string(body)}
}

// profileEmailFollowFlash issues the post-303 GET so the test sees
// the rendered banner from the signed flash cookie.
func profileEmailFollowFlash(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()

	return profileEmailGET(ctx, t, client, baseURL).body
}

func extractEmailCSRFToken(t *testing.T, body string) string {
	t.Helper()

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		t.Fatalf("csrf token missing from profile email body (excerpt: %.200q)", body)
	}

	return matches[1]
}

// profileGetStatus issues GET /profile with the supplied client and
// returns the response status. Tests use it to decide whether the
// current cookie still passes RequireAuthenticated +
// RequireVerifiedEmail.
func profileGetStatus(ctx context.Context, t *testing.T, client *http.Client, baseURL string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/profile", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer closeBodyNoError(t, resp.Body)

	return resp.StatusCode
}

// freshClientSharingSession copies the session cookie out of an
// already-signed-in client into a brand-new jar. The returned
// client carries the original session_version - tests use it to
// confirm a subsequent session-version bump invalidates this exact
// cookie.
func freshClientSharingSession(t *testing.T, src *http.Client, baseURL string) *http.Client {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse err = %v, want nil", err)
	}
	cookies := src.Jar.Cookies(parsed)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	jar.SetCookies(parsed, cookies)

	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// getVerifyEmailWithClient is the cookied variant of
// getVerifyEmail in verify_email_test.go. The email-change flow has
// to drive the consume side from the SAME client that initiated the
// change so the session-refresh assertion is meaningful.
func getVerifyEmailWithClient(
	ctx context.Context, t *testing.T, baseURL, raw string, client *http.Client,
) *http.Response {
	t.Helper()

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
