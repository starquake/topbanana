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
)

// TestProfile_Integration covers #410: the per-player profile page
// renders for authenticated visitors and lets them rename themselves.
// Drives the full HTTP stack — middleware, CSRF, template, store —
// against a real server.
//
//nolint:paralleltest,tparallel // subtests share the seeded admin and the same client jar; sequencing is intentional.
func TestProfile_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	t.Run("anonymous visitor redirected to login", func(t *testing.T) {
		client := authClient(t)
		// EnsurePlayer-style anonymous session: no cookie at all. The
		// middleware refuses without a session and 303s to /login.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/profile", nil)
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
		// #449: GET to a protected route now carries the original URI
		// as ?next= so the login flow can drop the visitor back on it.
		if got, want := resp.Header.Get("Location"), "/login?next=%2Fprofile"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	// Register an admin so the rest of the subtests have an
	// authenticated session to drive against.
	authn := authClient(t)
	registerVerifyAndMint(ctx, t, authn, srv.BaseURL, srv.DBURI, "profile-admin", "correct-battery-13")

	t.Run("GET /profile renders form with the current username", func(t *testing.T) {
		snap := profileGET(ctx, t, authn, srv.BaseURL)
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, `name="username"`) {
			t.Error("body missing username input")
		}
		// The value attribute must contain the signed-in admin's name
		// so the input arrives pre-filled.
		if !strings.Contains(snap.body, `value="profile-admin"`) {
			t.Error(`body missing value="profile-admin" on the username input`)
		}
	})

	t.Run("POST /profile/username with empty value returns 400", func(t *testing.T) {
		snap := profilePOST(ctx, t, authn, srv.BaseURL, "   ")
		if got, want := snap.status, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, "Display name is required.") {
			t.Error("body missing empty-display-name error message")
		}
	})

	t.Run("POST /profile/username with a taken value returns 409", func(t *testing.T) {
		// Register a second player so the admin can collide with them.
		registerForPending(ctx, t, authClient(t), srv.BaseURL, "rival-name", "correct-battery-13")

		snap := profilePOST(ctx, t, authn, srv.BaseURL, "rival-name")
		if got, want := snap.status, http.StatusConflict; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, "already taken") {
			t.Error("body missing taken-username error message")
		}
		// The attempted value sticks in the input so the user can edit
		// it instead of retyping.
		if !strings.Contains(snap.body, `value="rival-name"`) {
			t.Error(`body missing value="rival-name" on the username input after a collision`)
		}
	})

	t.Run("POST /profile/username with a fresh value renames the player", func(t *testing.T) {
		snap := profilePOST(ctx, t, authn, srv.BaseURL, "renamed-admin")
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d (body=%q)", got, want, snap.body)
		}
		if !strings.Contains(snap.body, "Display name updated.") {
			t.Error("body missing success banner")
		}
		if !strings.Contains(snap.body, `value="renamed-admin"`) {
			t.Error(`body missing value="renamed-admin" on the username input after a successful rename`)
		}
	})
}

// profilePageSnapshot is the trio of (status, body, csrf token) the
// profile tests assert on. Pulled out so the POST helper can reuse
// the GET helper's token extraction without callers having to
// thread cookies and parse responses themselves.
type profilePageSnapshot struct {
	status int
	body   string
	csrf   string
}

func profileGET(ctx context.Context, t *testing.T, client *http.Client, baseURL string) profilePageSnapshot {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	rendered := string(body)

	return profilePageSnapshot{
		status: resp.StatusCode,
		body:   rendered,
		csrf:   extractProfileCSRFToken(t, rendered),
	}
}

func profilePOST(ctx context.Context, t *testing.T, client *http.Client, baseURL, username string) profilePageSnapshot {
	t.Helper()

	// Re-fetch the page each time so the CSRF token paired with the
	// current nonce cookie is fresh.
	priming := profileGET(ctx, t, client, baseURL)
	form := url.Values{
		"csrf_token": {priming.csrf},
		"username":   {username},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/username", strings.NewReader(form.Encode()),
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

	return profilePageSnapshot{
		status: resp.StatusCode,
		body:   string(body),
	}
}

// extractProfileCSRFToken pulls the csrf_token hidden input out of a
// rendered profile page so the POST helper can resubmit a valid token.
func extractProfileCSRFToken(t *testing.T, body string) string {
	t.Helper()

	re := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		t.Fatalf("csrf token missing from profile body (excerpt: %.200q)", body)
	}

	return matches[1]
}

// closeBodyNoError mirrors the package's closeBody but suppresses
// t.Errorf chatter so subtests sharing a client don't pollute output
// when an already-failing assertion fires above.
func closeBodyNoError(t *testing.T, body io.Closer) {
	t.Helper()
	if cerr := body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
}
