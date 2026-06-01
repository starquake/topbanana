//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// TestProfilePassword_Integration drives the full /profile/password
// flow against a real server: happy-path rotation, wrong-current
// rejection, length checks, confirm mismatch, and the session_version
// bump that invalidates other live cookies (#499).
//
//nolint:paralleltest,tparallel // subtests share the registered admin and sequenced cookie jars; ordering is intentional.
func TestProfilePassword_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	displayName := "pw-change-admin"
	originalPassword := "correct-battery-13"
	updatedPassword := "fresh-passphrase-99"

	authn := authClient(t)
	registerVerifyAndMint(ctx, t, authn, srv.BaseURL, srv.DBURI, displayName, originalPassword)

	t.Run("anonymous visitor redirected to login", func(t *testing.T) {
		client := authClient(t)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/profile/password", nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do err = %v, want nil", err)
		}
		defer closeBodyNoError(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Location"), "/login?next=%2Fprofile%2Fpassword"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("GET /profile/password renders three password inputs", func(t *testing.T) {
		snap := passwordGET(ctx, t, authn, srv.BaseURL)
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		for _, attr := range []string{`name="current_password"`, `name="new_password"`, `name="new_password_confirm"`} {
			if !strings.Contains(snap.body, attr) {
				t.Errorf("body missing input %s", attr)
			}
		}
		for _, attr := range []string{`autocomplete="current-password"`, `autocomplete="new-password"`} {
			if !strings.Contains(snap.body, attr) {
				t.Errorf("body missing %s", attr)
			}
		}
	})

	t.Run("POST with wrong current password rejects and does not rotate", func(t *testing.T) {
		snap := passwordPOST(ctx, t, authn, srv.BaseURL, "totally-wrong-pass", updatedPassword, updatedPassword)
		if got, want := snap.status, http.StatusUnauthorized; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := snap.body, "Current password is incorrect."; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}

		// Old password must still work: login flow against the same credentials.
		probe := authClient(t)
		loc := loginForRedirect(ctx, t, probe, srv.BaseURL, displayName, originalPassword)
		if got, want := loc, "/admin/quizzes"; got != want {
			t.Errorf("post-reject login Location = %q, want %q (old password must still work)", got, want)
		}
	})

	t.Run("POST with too-short new password rejects", func(t *testing.T) {
		snap := passwordPOST(ctx, t, authn, srv.BaseURL, originalPassword, "shortie", "shortie")
		if got, want := snap.status, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := snap.body, "Password must be at least"; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	})

	t.Run("POST with too-long new password rejects", func(t *testing.T) {
		long := strings.Repeat("a", 73)
		snap := passwordPOST(ctx, t, authn, srv.BaseURL, originalPassword, long, long)
		if got, want := snap.status, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := snap.body, "Password must be at most"; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	})

	t.Run("POST with mismatched confirm rejects", func(t *testing.T) {
		snap := passwordPOST(ctx, t, authn, srv.BaseURL, originalPassword, updatedPassword, updatedPassword+"x")
		if got, want := snap.status, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := snap.body, "Passwords do not match."; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	})

	// Happy-path + cross-session invalidation in a single subtest so the
	// "old cookie now bounces" assertion runs against the same rotation
	// that produced the new password.
	t.Run("happy path rotates and invalidates other sessions", func(t *testing.T) {
		// Sign in a second client (jar B) against the same account
		// BEFORE we rotate. Its cookie carries the pre-rotation
		// session_version stamp; the rotation must invalidate it.
		other := authClient(t)
		// Earlier subtests left the localhost-peer login-limiter
		// bucket hot; let the 3s cooldown (#494) elapse before the
		// next /login POST or this one gets served as 429.
		time.Sleep(auth.LoginCooldown() + 100*time.Millisecond)
		loc := loginForRedirect(ctx, t, other, srv.BaseURL, displayName, originalPassword)
		if got, want := loc, "/admin/quizzes"; got != want {
			t.Fatalf("second client pre-rotation login Location = %q, want %q", got, want)
		}

		// Confirm jar B is signed in before the rotation.
		preBounce := getWith(ctx, t, other, srv.BaseURL+"/admin/quizzes")
		preBounce.Body.Close() //nolint:errcheck // cleanup.
		if got, want := preBounce.StatusCode, http.StatusOK; got != want {
			t.Fatalf("pre-rotation jar B /admin/quizzes status = %d, want %d", got, want)
		}

		// Rotate via jar A.
		snap := passwordPOST(ctx, t, authn, srv.BaseURL, originalPassword, updatedPassword, updatedPassword)
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d (body=%q)", got, want, snap.body)
		}
		if got, want := snap.body, "Password updated."; !strings.Contains(got, want) {
			t.Errorf("body missing success banner %q", want)
		}

		// Jar A must keep working - the handler refreshed its cookie.
		stillAlive := getWith(ctx, t, authn, srv.BaseURL+"/admin/quizzes")
		stillAlive.Body.Close() //nolint:errcheck // cleanup.
		if got, want := stillAlive.StatusCode, http.StatusOK; got != want {
			t.Errorf("post-rotation jar A status = %d, want %d", got, want)
		}

		// Jar B's old cookie carried the pre-rotation session_version
		// stamp; the bump invalidates it and the protected route 303s
		// to /login.
		bounce := getWith(ctx, t, other, srv.BaseURL+"/admin/quizzes")
		defer bounce.Body.Close() //nolint:errcheck // cleanup.
		if got, want := bounce.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("post-rotation jar B status = %d, want %d", got, want)
		}
		if bounceLoc := bounce.Header.Get("Location"); !strings.HasPrefix(bounceLoc, "/login") {
			t.Errorf("post-rotation jar B Location = %q, want /login...", bounceLoc)
		}

		// Old password must no longer work.
		// Wait out the login-limiter cooldown again before the next /login.
		time.Sleep(auth.LoginCooldown() + 100*time.Millisecond)
		probeOld := authClient(t)
		token := fetchCSRFToken(ctx, t, probeOld, srv.BaseURL+"/login")
		oldStatus := postLoginRaw(ctx, t, probeOld, srv.BaseURL, displayName, originalPassword, token)
		if got, want := oldStatus, http.StatusUnauthorized; got != want {
			t.Errorf("old-password login status = %d, want %d", got, want)
		}

		// New password must work.
		time.Sleep(auth.LoginCooldown() + 100*time.Millisecond)
		probeNew := authClient(t)
		loc = loginForRedirect(ctx, t, probeNew, srv.BaseURL, displayName, updatedPassword)
		if got, want := loc, "/admin/quizzes"; got != want {
			t.Errorf("new-password login Location = %q, want %q", got, want)
		}
	})
}

type passwordPageSnapshot struct {
	status int
	body   string
	csrf   string
}

func passwordGET(ctx context.Context, t *testing.T, client *http.Client, baseURL string) passwordPageSnapshot {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/profile/password", nil)
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

	return passwordPageSnapshot{
		status: resp.StatusCode,
		body:   rendered,
		csrf:   extractPasswordCSRFToken(t, rendered),
	}
}

func passwordPOST(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, currentPW, newPW, confirmPW string,
) passwordPageSnapshot {
	t.Helper()

	priming := passwordGET(ctx, t, client, baseURL)
	form := url.Values{
		"csrf_token":           {priming.csrf},
		"current_password":     {currentPW},
		"new_password":         {newPW},
		"new_password_confirm": {confirmPW},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/password", strings.NewReader(form.Encode()),
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

	return passwordPageSnapshot{
		status: resp.StatusCode,
		body:   string(body),
	}
}

func extractPasswordCSRFToken(t *testing.T, body string) string {
	t.Helper()

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		t.Fatalf("csrf token missing from password body (excerpt: %.200q)", body)
	}

	return matches[1]
}

// postLoginRaw posts the login form once and returns the response
// status. Used to assert "the old password no longer logs in" without
// asserting on the Location header (a failed login renders the form
// in place with a 401, not a 303).
func postLoginRaw(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName, password, csrfToken string,
) int {
	t.Helper()

	form := url.Values{}
	form.Add("displayName", displayName)
	form.Add("email", displayName+"@example.test")
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
	defer closeBodyNoError(t, resp.Body)

	return resp.StatusCode
}
