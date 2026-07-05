package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestRegister_PasswordMismatchRejected pins the reg-mismatch case from
// docs/obsolete/auth-manual-test-plan.md: a register POST whose password and
// confirmation differ is rejected with the mismatch message and no
// account is created.
func TestRegister_PasswordMismatchRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	status, body := registerWithConfirm(
		ctx, t, authClient(t), srv.BaseURL,
		"reg-mismatch", "reg-mismatch@example.test", "correct-battery-13", "different-battery-13",
	)

	if got, want := status, http.StatusBadRequest; got != want {
		t.Errorf("register status = %d, want %d", got, want)
	}
	if got, want := body, "Passwords do not match."; !strings.Contains(got, want) {
		t.Errorf("register body missing mismatch message %q; body=%.300q", want, got)
	}

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.
	if _, err := stores.Players.GetPlayerByDisplayName(ctx, "reg-mismatch"); err == nil {
		t.Error("GetPlayerByDisplayName err = nil, want not-found (mismatch must not create an account)")
	}
}

// TestRegister_DuplicateDisplayNameRejected pins the reg-dupname case: a
// display name is a public handle, not an enumeration secret, so reusing
// one reports the collision with a 409 instead of the opaque pending page.
func TestRegister_DuplicateDisplayNameRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	// Seed the owner so the display name is taken.
	registerForPending(ctx, t, authClient(t), srv.BaseURL, "dupname-owner", "correct-battery-13")

	// Reuse the display name under a fresh email.
	status, body := registerRaw(
		ctx, t, authClient(t), srv.BaseURL, "dupname-owner", "dupname-other@example.test", "correct-battery-13",
	)

	if got, want := status, http.StatusConflict; got != want {
		t.Errorf("duplicate-name register status = %d, want %d", got, want)
	}
	if got, want := body, "That display name is already taken."; !strings.Contains(got, want) {
		t.Errorf("register body missing taken-name message %q; body=%.300q", want, got)
	}
}

// TestRegister_DisabledReturns404 pins the reg-disabled case: with
// REGISTRATION_ENABLED unset the /register routes are never registered,
// so both the form and the submit 404 from the mux.
func TestRegister_DisabledReturns404(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	client := authClient(t)

	getResp := doRequest(ctx, t, client, http.MethodGet, srv.BaseURL+"/register", nil)
	getResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := getResp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("GET /register status = %d, want %d (registration disabled)", got, want)
	}

	form := url.Values{}
	form.Add("display_name", "reg-disabled")
	form.Add("email", "reg-disabled@example.test")
	form.Add("password", "correct-battery-13")
	form.Add("password_confirm", "correct-battery-13")
	postResp := doRequest(ctx, t, client, http.MethodPost, srv.BaseURL+"/register", strings.NewReader(form.Encode()))
	postResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := postResp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("POST /register status = %d, want %d (registration disabled)", got, want)
	}
}

// TestLogin_BadPasswordAndUnknownEmailAreIndistinguishable pins the
// login-badpass / login-unknown enumeration contract: a wrong password
// on a real account and an unknown email return byte-identical responses
// (once the echoed email is normalised out), so /login is not an oracle
// for which addresses have accounts.
func TestLogin_BadPasswordAndUnknownEmailAreIndistinguishable(t *testing.T) {
	t.Parallel()

	// LOGIN_COOLDOWN=0s so the two back-to-back same-IP logins below do
	// not trip the per-IP limiter (#494) and diverge into a 429.
	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"LOGIN_COOLDOWN":       "0s",
	})

	const (
		realEmail = "login-enum-real@example.test"
		realPass  = "correct-battery-real-13"
	)
	registerForPending(ctx, t, authClient(t), srv.BaseURL, "login-enum-real", realPass)
	verifyPlayerEmail(ctx, t, srv.DBURI, "login-enum-real")

	// One client for both probes so the rendered login form carries the
	// same CSRF nonce - otherwise the per-client csrf_token field is the
	// only byte that differs and the opacity compare gets a false miss.
	client := authClient(t)
	badPassStatus, badPassBody := loginStatusBody(ctx, t, client, srv.BaseURL, realEmail, "wrong-battery-13")
	unknownStatus, unknownBody := loginStatusBody(
		ctx, t, client, srv.BaseURL, "login-enum-absent@example.test", "wrong-battery-13",
	)

	if got, want := badPassStatus, http.StatusUnauthorized; got != want {
		t.Errorf("bad-password status = %d, want %d", got, want)
	}
	if got, want := unknownStatus, badPassStatus; got != want {
		t.Errorf("unknown-email status = %d, want %d (bad-password)", got, want)
	}

	badNorm := strings.ReplaceAll(badPassBody, realEmail, "EMAIL")
	unknownNorm := strings.ReplaceAll(unknownBody, "login-enum-absent@example.test", "EMAIL")
	if badNorm != unknownNorm {
		t.Errorf("login responses differ after email normalisation:\nbadpass=%.400q\nunknown=%.400q",
			badNorm, unknownNorm)
	}
}

// TestLogin_RejectsOpenRedirectNext pins the next-openredirect case: a
// crafted off-site next value is dropped, so a successful login lands on
// the role's safe default landing path rather than the external URL.
func TestLogin_RejectsOpenRedirectNext(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"LOGIN_COOLDOWN":       "0s",
	})

	const (
		displayName = "redir-player"
		password    = "correct-battery-redir-13"
	)
	registerForPending(ctx, t, authClient(t), srv.BaseURL, displayName, password)
	verifyPlayerEmail(ctx, t, srv.DBURI, displayName)

	// The off-site next must be dropped: a successful login lands on a
	// same-site path (the role landing), never the external target. The
	// exact landing depends on the role the registrant is assigned, so
	// assert the security property - stayed on-site - rather than a fixed
	// path.
	for _, next := range []string{"https://evil.example", "//evil.example", "/\\evil.example"} {
		location := loginWithNext(ctx, t, authClient(t), srv.BaseURL, displayName+"@example.test", password, next)
		if !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") {
			t.Errorf("login next=%q Location = %q, want a same-site path", next, location)
		}
		if strings.Contains(location, "evil.example") {
			t.Errorf("login next=%q Location = %q leaked the off-site target", next, location)
		}
	}
}

// TestCSRF_RejectsMissingAndBadToken pins the csrf hardening spot check:
// an unsafe POST without a valid token is rejected with 403 before the
// handler runs.
func TestCSRF_RejectsMissingAndBadToken(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	// No prior GET, so the client carries neither the nonce cookie nor a
	// token: the double-submit check fails.
	missing := url.Values{}
	missing.Add("email", "csrf@example.test")
	missing.Add("password", "correct-battery-13")
	missingResp := doRequest(
		ctx, t, authClient(t), http.MethodPost, srv.BaseURL+"/login", strings.NewReader(missing.Encode()),
	)
	missingResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := missingResp.StatusCode, http.StatusForbidden; got != want {
		t.Errorf("POST /login without CSRF token status = %d, want %d", got, want)
	}

	// A client that GETs the form first holds a valid nonce cookie, but a
	// garbage token still fails the constant-time compare.
	client := authClient(t)
	fetchCSRFToken(ctx, t, client, srv.BaseURL+"/register")
	bad := url.Values{}
	bad.Add("display_name", "csrf-bad")
	bad.Add("email", "csrf-bad@example.test")
	bad.Add("password", "correct-battery-13")
	bad.Add("password_confirm", "correct-battery-13")
	bad.Add("csrf_token", "not-a-valid-token")
	badResp := doRequest(
		ctx, t, client, http.MethodPost, srv.BaseURL+"/register", strings.NewReader(bad.Encode()),
	)
	badResp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := badResp.StatusCode, http.StatusForbidden; got != want {
		t.Errorf("POST /register with bad CSRF token status = %d, want %d", got, want)
	}
}

// registerWithConfirm POSTs /register with an explicit password
// confirmation field, so a caller can drive the mismatch branch that
// registerRaw (confirm == password) cannot reach. Returns status + body.
func registerWithConfirm(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName, email, password, confirm string,
) (int, string) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")

	form := url.Values{}
	form.Add("display_name", displayName)
	form.Add("email", email)
	form.Add("password", password)
	form.Add("password_confirm", confirm)
	form.Add("csrf_token", token)

	resp := doRequest(ctx, t, client, http.MethodPost, baseURL+"/register", strings.NewReader(form.Encode()))
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return resp.StatusCode, string(body)
}

// loginStatusBody POSTs /login with a fetched CSRF token and returns the
// response status and body. Used by the enumeration-equivalence check.
func loginStatusBody(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, email, password string,
) (int, string) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/login")

	form := url.Values{}
	form.Add("email", email)
	form.Add("password", password)
	form.Add("csrf_token", token)

	resp := doRequest(ctx, t, client, http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()))
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return resp.StatusCode, string(body)
}

// loginWithNext POSTs /login carrying a next form field and returns the
// 303 Location header. Fails the test if the login does not redirect, so
// the caller can focus on asserting the redirect target.
func loginWithNext(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, email, password, next string,
) string {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/login")

	form := url.Values{}
	form.Add("email", email)
	form.Add("password", password)
	form.Add("next", next)
	form.Add("csrf_token", token)

	resp := doRequest(ctx, t, client, http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()))
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("login POST status = %d, want %d", got, want)
	}

	return resp.Header.Get("Location")
}

// doRequest builds and sends a request with the form content type and
// returns the raw response. Centralises the NewRequestWithContext +
// client.Do boilerplate the gap tests repeat.
func doRequest(
	ctx context.Context, t *testing.T, client *http.Client, method, target string, body io.Reader,
) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}
