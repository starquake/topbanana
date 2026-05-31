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
	"github.com/starquake/topbanana/internal/session"
)

// TestResetPassword_HappyPath registers a player, mints a reset token
// against the store, posts the new password to /reset-password, and
// asserts the rotation lands, the reset-token holder is auto-logged-in
// onto their role landing, and every OTHER session is invalidated. The
// new login with the new password is exercised end-to-end.
func TestResetPassword_HappyPath(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	// First password-bearing registrant is promoted to admin, so the
	// reset-token holder's role landing is /admin/quizzes. The signed
	// client holds a live session at the pre-reset version so the test
	// can prove the reset's session_version bump bounces it.
	signed := authClient(t)
	registerVerifyAndMint(ctx, t, signed, srv.BaseURL, srv.DBURI, "reset-happy", "old-pass-12345")

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByUsername(ctx, "reset-happy")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	startingVersion := player.SessionVersion

	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	// POST a fresh password through a cookieless client so we exercise
	// the exact unauthenticated path the email link funnels users into.
	client := authClient(t)
	resp := postReset(ctx, t, client, srv.BaseURL, raw, "new-pass-67890")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	// Auto-login lands the holder on their role page, not /login.
	if got, want := resp.Header.Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if !hasSessionCookie(resp) {
		t.Errorf("reset response did not set a %q cookie; auto-login must mint a session", session.CookieName)
	}

	refreshed, err := stores.Players.GetPlayerByID(ctx, player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := refreshed.SessionVersion, startingVersion+1; got != want {
		t.Errorf("session_version = %d, want %d (reset must bump)", got, want)
	}

	// The auto-login cookie carries the post-rotation version, so the
	// holder can reach a gated page without bouncing to /login.
	gated := getWith(ctx, t, client, srv.BaseURL+"/admin/quizzes")
	defer gated.Body.Close() //nolint:errcheck // cleanup.
	if got, want := gated.StatusCode, http.StatusOK; got != want {
		t.Errorf("post-reset gated status = %d, want %d (auto-login must reach the landing)", got, want)
	}

	// The signed-in client's old cookie carried the pre-reset version;
	// /admin/quizzes must now redirect them to /login.
	bounce := getWith(ctx, t, signed, srv.BaseURL+"/admin/quizzes")
	defer bounce.Body.Close() //nolint:errcheck // cleanup.
	if got, want := bounce.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("post-reset admin status = %d, want %d", got, want)
	}
	if loc := bounce.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("post-reset admin Location = %q, want /login...", loc)
	}

	// Logging in with the new password must succeed.
	login := authClient(t)
	loc := loginForRedirect(ctx, t, login, srv.BaseURL, "reset-happy", "new-pass-67890")
	if got, want := loc, "/admin/quizzes"; got != want {
		t.Errorf("post-reset login Location = %q, want %q", got, want)
	}
}

// hasSessionCookie reports whether resp sets a non-empty session
// cookie. A cleared session writes the same cookie name with an empty
// value + MaxAge<0, so an empty value does not count as a live session.
func hasSessionCookie(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			return true
		}
	}

	return false
}

// TestResetPassword_RejectsConsumedReplay pins the single-use rule:
// a second POST with the same token returns the "link is no longer
// valid" page.
func TestResetPassword_RejectsConsumedReplay(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-replay", "reset-replay@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	first := postReset(ctx, t, authClient(t), srv.BaseURL, raw, "first-new-pass-12345")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := postReset(ctx, t, authClient(t), srv.BaseURL, raw, "second-new-pass-12345")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusGone; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Link is no longer valid"; !strings.Contains(got, want) {
		t.Errorf("body missing %q", want)
	}
}

// TestResetPassword_PasswordTooShort hits the form-validation branch:
// a too-short password is rejected with a 400 + banner and the token
// stays consumable (so the user can retry without re-requesting).
func TestResetPassword_PasswordTooShort(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-short", "reset-short@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	resp := postReset(ctx, t, authClient(t), srv.BaseURL, raw, "shortie")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	// Retry with a good password - the token must still be live.
	retry := postReset(ctx, t, authClient(t), srv.BaseURL, raw, "good-new-pass-12345")
	retry.Body.Close() //nolint:errcheck // cleanup.
	if got, want := retry.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("retry status = %d, want %d", got, want)
	}
}

// TestResetForm_DeadTokenRendersInvalid pins the #472 GET preflight:
// a consumed or expired token must short-circuit to the "link is no
// longer valid" page so the user is not asked to type a password the
// POST would reject.
func TestResetForm_DeadTokenRendersInvalid(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-dead", "reset-dead@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	// expires_at in the past: the preflight must read this as not-live.
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(-time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	target := srv.BaseURL + "/reset-password?" + url.Values{"token": {raw}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := authClient(t).Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Link is no longer valid"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

// TestResetForm_LiveTokenRendersForm pins the live-token branch of
// the preflight: the GET handler hands the form to the user when the
// row is unconsumed and unexpired.
func TestResetForm_LiveTokenRendersForm(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.CreatePlayer(
		ctx, "reset-live", "reset-live@example.test", "h", "player",
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw, hash, err := auth.GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if cerr := stores.ResetTokens.CreateResetToken(ctx, hash, player.ID, time.Now().Add(time.Hour)); cerr != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", cerr)
	}

	target := srv.BaseURL + "/reset-password?" + url.Values{"token": {raw}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := authClient(t).Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), `name="token"`; !strings.Contains(got, want) {
		t.Errorf("body should render form token field %q", want)
	}
}

// postReset issues POST /reset-password with a freshly-fetched CSRF
// token on the supplied client and returns the raw response. The CSRF
// token is fetched from /login so the helper works regardless of the
// reset-password GET preflight short-circuiting on a dead token (#472).
func postReset(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, rawToken, password string,
) *http.Response {
	t.Helper()

	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/login")

	form := url.Values{}
	form.Add("csrf_token", csrfToken)
	form.Add("token", rawToken)
	form.Add("password", password)
	form.Add("confirm", password)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/reset-password", strings.NewReader(form.Encode()),
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
