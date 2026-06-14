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

	t.Run("GET /profile renders form with the current displayName", func(t *testing.T) {
		snap := profileGET(ctx, t, authn, srv.BaseURL)
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, `name="display_name"`) {
			t.Error("body missing display name input")
		}
		// The value attribute must contain the signed-in admin's name
		// so the input arrives pre-filled.
		if !strings.Contains(snap.body, `value="profile-admin"`) {
			t.Error(`body missing value="profile-admin" on the displayName input`)
		}
		// The account section links to the change-email and
		// change-password flows; these render regardless of role.
		if !strings.Contains(snap.body, `href="/profile/email"`) {
			t.Error(`body missing href="/profile/email" link`)
		}
		if !strings.Contains(snap.body, "Change email") {
			t.Error(`body missing "Change email" link label`)
		}
		if !strings.Contains(snap.body, `href="/profile/password"`) {
			t.Error(`body missing href="/profile/password" link`)
		}
		if !strings.Contains(snap.body, "Change password") {
			t.Error(`body missing "Change password" link label`)
		}
	})

	t.Run("POST /profile/display-name with empty value returns 400", func(t *testing.T) {
		snap := profilePOST(ctx, t, authn, srv.BaseURL, "   ")
		if got, want := snap.status, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, "Display name is required.") {
			t.Error("body missing empty-display-name error message")
		}
	})

	t.Run("POST /profile/display-name with a taken value returns 409", func(t *testing.T) {
		// Register a second player so the admin can collide with them.
		registerForPending(ctx, t, authClient(t), srv.BaseURL, "rival-name", "correct-battery-13")

		snap := profilePOST(ctx, t, authn, srv.BaseURL, "rival-name")
		if got, want := snap.status, http.StatusConflict; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if !strings.Contains(snap.body, "already taken") {
			t.Error("body missing taken-displayName error message")
		}
		// The attempted value sticks in the input so the user can edit
		// it instead of retyping.
		if !strings.Contains(snap.body, `value="rival-name"`) {
			t.Error(`body missing value="rival-name" on the displayName input after a collision`)
		}
	})

	t.Run("POST /profile/display-name with a fresh value renames the player", func(t *testing.T) {
		snap := profilePOST(ctx, t, authn, srv.BaseURL, "renamed-admin")
		if got, want := snap.status, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d (body=%q)", got, want, snap.body)
		}
		if !strings.Contains(snap.body, "Display name updated.") {
			t.Error("body missing success banner")
		}
		if !strings.Contains(snap.body, `value="renamed-admin"`) {
			t.Error(`body missing value="renamed-admin" on the displayName input after a successful rename`)
		}
	})

	// #732: arriving from the admin chrome (?next=/admin) points the
	// back link at the dashboard and stamps a hidden next field so the
	// return target survives the rename POST.
	t.Run("GET /profile?next=/admin returns to the admin dashboard", func(t *testing.T) {
		body := profileGETURL(ctx, t, authn, srv.BaseURL+"/profile?next=%2Fadmin")
		if !strings.Contains(body, `href="/admin"`) {
			t.Error(`body missing href="/admin" back link`)
		}
		if !strings.Contains(body, "Back to admin") {
			t.Error(`body missing "Back to admin" label`)
		}
		if !strings.Contains(body, `name="next" value="/admin"`) {
			t.Error(`body missing hidden next field carrying /admin`)
		}
	})

	// An off-allowlist next (external host, or a non-admin internal
	// path) must not ride the back link - it falls back to home so the
	// param cannot be turned into an open or surprise redirect.
	t.Run("GET /profile with an unsafe next falls back to home", func(t *testing.T) {
		for _, next := range []string{"https%3A%2F%2Fevil.example", "%2Fprofile%2Femail", "%2F%2Fevil.example"} {
			body := profileGETURL(ctx, t, authn, srv.BaseURL+"/profile?next="+next)
			if strings.Contains(body, "Back to admin") {
				t.Errorf("next=%q leaked a Back to admin link", next)
			}
			if !strings.Contains(body, "Back to home") {
				t.Errorf("next=%q did not fall back to the home link", next)
			}
		}
	})
}

// profileGETURL fetches an arbitrary profile URL (so callers can pass a
// ?next= query) and returns the response body. Unlike profileGET it
// does not extract a CSRF token, since the back-link assertions only
// read the rendered form.
func profileGETURL(ctx context.Context, t *testing.T, client *http.Client, rawURL string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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

	return string(body)
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

func profilePOST(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, displayName string,
) profilePageSnapshot {
	t.Helper()

	// Re-fetch the page each time so the CSRF token paired with the
	// current nonce cookie is fresh.
	priming := profileGET(ctx, t, client, baseURL)
	form := url.Values{
		"csrf_token":   {priming.csrf},
		"display_name": {displayName},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/display-name", strings.NewReader(form.Encode()),
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
