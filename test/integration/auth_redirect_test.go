package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
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

	// Register no longer redirects (#574 hard gate): it renders the
	// "verify your email" confirmation page with 200 and no session.
	// The login subtests below pin the per-role redirect instead.
	t.Run("admin register renders pending page", func(t *testing.T) {
		client := authClient(t)
		registerForPending(ctx, t, client, baseURL, "redirect-admin", "redirect-admin-pass-123")
	})

	t.Run("player register renders pending page", func(t *testing.T) {
		client := authClient(t)
		registerForPending(ctx, t, client, baseURL, "redirect-player", "redirect-player-pass-123")
	})

	// Stamp email_verified_at on both registered rows so the post-#492
	// verify-gate at /login (HandleLoginSubmit) lets them through.
	// Pre-#492 these subtests ran against a gate-free /login; now they
	// pin the post-verify happy path.
	verifyPlayerEmail(ctx, t, srv.DBURI, "redirect-admin")
	verifyPlayerEmail(ctx, t, srv.DBURI, "redirect-player")

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

// testSessionKey is the fixed signing key startServer sets as the
// default SESSION_KEY (see testmain_test.go) so tests can mint a
// matching session cookie with mintSessionCookie. The hard
// email-verification gate (#574) means register and login no longer
// hand out a session for an unverified account, so the verify-gate
// tests forge the signed-in-but-unverified state directly instead.
const testSessionKey = "integration-test-session-key-0123456789abcdef"

// mintSessionCookie signs a session cookie for the named player using
// testSessionKey and installs it on client's jar, putting the client in
// the signed-in state without going through login. startServer signs
// cookies with testSessionKey by default, so the minted cookie verifies
// unless the test overrode SESSION_KEY.
func mintSessionCookie(ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName string) {
	t.Helper()

	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("mintSessionCookie GetPlayerByDisplayName err = %v, want nil", err)
	}

	rec := httptest.NewRecorder()
	session.New([]byte(testSessionKey), false).Set(rec, player.ID, player.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse err = %v, want nil", err)
	}
	client.Jar.SetCookies(parsed, []*http.Cookie{cookie})
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

// registerForPending POSTs /register and asserts the post-#574
// hard-gate contract: 200, the "Verify your email" confirmation body
// naming the recipient, and no live session cookie on the jar. Returns
// nothing - the registered row exists in the DB but the visitor is not
// signed in until they verify and log in (see registerVerifyAndSignIn).
func registerForPending(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName, password string,
) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")
	resp := postRegister(ctx, t, client, baseURL, token, displayName, password)
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Verify your email"; !strings.Contains(got, want) {
		t.Errorf("register body missing confirmation headline %q; body=%.300q", want, got)
	}
	if got, want := string(body), displayName+"@example.test"; !strings.Contains(got, want) {
		t.Errorf("register body missing recipient address %q; body=%.300q", want, got)
	}
	assertNoLiveSession(t, resp)
}

// assertNoLiveSession fails when resp carries a session cookie with a
// non-empty value. A deletion cookie (empty value, negative MaxAge)
// emitted by sessions.Clear is fine - that is the hard gate signing the
// anonymous-upgrade path out.
func assertNoLiveSession(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("live session cookie set on register: %+v", c)
		}
	}
}

// registerVerifyAndSignIn registers displayName through /register, stamps
// email_verified_at directly in the DB (bypassing the email channel),
// then logs in so client's cookie jar carries a live session. This is
// the post-#574 replacement for the old "register => signed in"
// behaviour that many tests relied on: the hard gate means a session is
// only obtained after verification + login.
func registerVerifyAndSignIn(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName, password string,
) {
	t.Helper()

	registerForPending(ctx, t, client, baseURL, displayName, password)
	verifyPlayerEmail(ctx, t, dbURI, displayName)
	loginForRedirect(ctx, t, client, baseURL, displayName, password)
}

// registerVerifyAndMint registers displayName, stamps email_verified_at in
// the DB, and mints a session cookie onto client's jar without going
// through /login. Use this instead of registerVerifyAndSignIn when a
// single test signs in several accounts against one server: minting
// sidesteps the per-IP login cooldown (#494) that back-to-back logins
// would trip. Relies on startServer's default SESSION_KEY.
func registerVerifyAndMint(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName, password string,
) {
	t.Helper()

	registerForPending(ctx, t, client, baseURL, displayName, password)
	verifyPlayerEmail(ctx, t, dbURI, displayName)
	mintSessionCookie(ctx, t, client, baseURL, dbURI, displayName)
}

// registerVerifyViaLinkAndMint is registerVerifyAndMint's
// real-verify-flow variant: it proves the email through GET /verify-email
// (verifyPlayerEmailViaLink) instead of a direct DB stamp, so the
// verify-time ADMIN_EMAILS promotion (#785) actually fires. Use it when
// the account must end up an admin via the allowlist rather than the
// first-registrant rule.
func registerVerifyViaLinkAndMint(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName, password string,
) {
	t.Helper()

	registerForPending(ctx, t, client, baseURL, displayName, password)
	verifyPlayerEmailViaLink(ctx, t, baseURL, dbURI, displayName)
	mintSessionCookie(ctx, t, client, baseURL, dbURI, displayName)
}

// verifyPlayerEmailViaLink proves the named player's email through the
// real GET /verify-email endpoint rather than a direct DB stamp. Mints a
// verify token, consumes it over HTTP, and asserts the 200. Use this
// (not verifyPlayerEmail) when the test depends on the verify-time side
// effects the endpoint applies - notably the ADMIN_EMAILS promotion
// (#785), which a direct DB stamp bypasses.
func verifyPlayerEmailViaLink(
	ctx context.Context, t *testing.T, baseURL, dbURI, displayName string,
) {
	t.Helper()

	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("verifyPlayerEmailViaLink GetPlayerByDisplayName err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateVerifyToken()
	if err != nil {
		t.Fatalf("verifyPlayerEmailViaLink GenerateVerifyToken err = %v, want nil", err)
	}
	if err := stores.VerifyTokens.CreateVerifyToken(ctx, hash, player.ID, futureHour(), ""); err != nil {
		t.Fatalf("verifyPlayerEmailViaLink CreateVerifyToken err = %v, want nil", err)
	}

	resp := httpGet(ctx, t, authClient(t), baseURL+"/verify-email?"+url.Values{"token": {raw}}.Encode())
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("verifyPlayerEmailViaLink verify status = %d, want %d", got, want)
	}
}

// registerAndMint registers displayName and mints a session cookie onto
// client's jar WITHOUT stamping email_verified_at, leaving the player
// signed in but unverified. Use it when a test needs a signed-in row
// that must stay in the unverified bucket. Relies on startServer's
// default SESSION_KEY.
func registerAndMint(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName, password string,
) {
	t.Helper()

	registerForPending(ctx, t, client, baseURL, displayName, password)
	mintSessionCookie(ctx, t, client, baseURL, dbURI, displayName)
}

// loginForRedirect POSTs /login and returns the Location header from
// the 303 response.
func loginForRedirect(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName, password string,
) string {
	t.Helper()

	return submitAuthForm(ctx, t, client, baseURL, "/login", displayName, password)
}

// submitAuthForm runs the GET-CSRF + POST-form dance for the /login
// redirect probe and asserts the response is 303 (anything else means
// the auth flow itself failed, not the redirect rule). The email
// credential is derived from displayName so a row registered as
// displayName@example.test logs in cleanly.
func submitAuthForm(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, path, displayName, password string,
) string {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+path)

	form := url.Values{}
	form.Add("email", displayName+"@example.test")
	form.Add("password", password)
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
