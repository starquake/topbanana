package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// brandLink is the shared top bar's brand link. Its aria-label pins the
// accessible name "Top Banana!" that the e2e role locators depend on, so
// the integration tests assert the exact attribute.
const brandLink = `aria-label="Top Banana!"`

// footerFragment returns the metadata footer slice of a rendered page so
// the footer-only assertions do not accidentally match controls the top
// bar carries. The site footer is the last <footer> on the page.
func footerFragment(t *testing.T, body string) string {
	t.Helper()

	idx := strings.LastIndex(body, "<footer")
	if idx == -1 {
		t.Fatalf("page has no <footer> (body excerpt: %.200q)", body)
	}

	return body[idx:]
}

// TestTopbar_HomeAnonymous pins the shared top bar on the anonymous home
// page (#844): the brand link, a Log in link in the account cluster, the
// Manage cross-link to /admin, and a metadata-only footer with no session
// controls.
func TestTopbar_HomeAnonymous(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	body := getBody(ctx, t, srv.BaseURL+"/")

	for _, want := range []string{
		brandLink,
		// Anonymous account cluster: a Log in link, no signed-in identity.
		`href="/login"`,
		// Cross-link back to the admin console. The label stays "Manage
		// quizzes" so the e2e role locator keeps resolving (#844).
		`href="/admin"`,
		"Manage quizzes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home top bar missing %q", want)
		}
	}
	if strings.Contains(body, "Signed in as") {
		t.Error("anonymous home should not render a signed-in identity")
	}
	if strings.Contains(body, `action="/logout"`) {
		t.Error("anonymous home should not render a log-out form")
	}

	// The footer is metadata only: it names the build + brand, never the
	// session controls (those moved into the top bar).
	footer := footerFragment(t, body)
	if !strings.Contains(footer, "Top Banana!") {
		t.Error("site footer missing the Top Banana! wordmark")
	}
	for _, banned := range []string{`action="/logout"`, "Signed in as", `href="/login"`, `href="/admin"`} {
		if strings.Contains(footer, banned) {
			t.Errorf("site footer must be metadata only, but contains %q", banned)
		}
	}
}

// TestTopbar_HomeSignedIn pins the signed-in account cluster on the home
// top bar: the viewer's display name links to /profile, a log-out form
// POSTs to /logout, and the anonymous Log in link is gone. The footer
// stays metadata only.
func TestTopbar_HomeSignedIn(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerAndMint(ctx, t, client, srv.BaseURL, srv.DBURI, "topbar-home-user", "correct-battery-13")

	snap := fetchWithClient(ctx, t, client, srv.BaseURL+"/")
	if got, want := snap.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	for _, want := range []string{
		brandLink,
		"Signed in as",
		"topbar-home-user",
		`href="/profile"`,
		`action="/logout"`,
		// The cross-link to the console stays present while signed in.
		"Manage quizzes",
	} {
		if !strings.Contains(snap.Body, want) {
			t.Errorf("signed-in home top bar missing %q", want)
		}
	}
	if strings.Contains(snap.Body, `href="/login"`) {
		t.Error("signed-in home should not render the anonymous Log in link")
	}

	footer := footerFragment(t, snap.Body)
	if strings.Contains(footer, `action="/logout"`) || strings.Contains(footer, "Signed in as") {
		t.Error("signed-in home footer must be metadata only")
	}
}

// TestTopbar_LoginPage pins the shared top bar on an auth surface (the
// login page): the viewer is anonymous, so the account cluster shows Log
// in and the cross-link shows Manage. The logo points home (logoHref "/"),
// not at /admin.
func TestTopbar_LoginPage(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	body := getBody(ctx, t, srv.BaseURL+"/login")

	for _, want := range []string{
		brandLink,
		`href="/admin"`,
		"Manage quizzes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login page top bar missing %q", want)
		}
	}
	// The brand link on an auth surface points home, not at the console.
	if !strings.Contains(body, `href="/" aria-label="Top Banana!"`) {
		t.Error("login page brand link should point at / (logoHref)")
	}
	if strings.Contains(body, "Signed in as") {
		t.Error("anonymous login page should not render a signed-in identity")
	}
}

// TestTopbar_AdminConsole pins the shared top bar on an admin page: the
// brand link points at /admin (logoHref), the section nav is present, the
// account cluster shows the signed-in admin plus a log-out form, and the
// cross-link is "View site" -> /. The admin footer is metadata only - the
// log out moved into the top bar.
func TestTopbar_AdminConsole(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "topbar-admin", "correct-battery-13")

	snap := fetchWithClient(ctx, t, client, srv.BaseURL+"/admin/quizzes")
	if got, want := snap.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	for _, want := range []string{
		brandLink,
		// Admin brand link points at the console.
		`href="/admin" aria-label="Top Banana!"`,
		// Section nav (admin-only).
		`href="/admin/quizzes"`,
		// Signed-in account cluster with the log-out form.
		"Signed in as",
		"topbar-admin",
		`action="/logout"`,
		// The admin profile link keeps the ?next=/admin round-trip so
		// saving in profile returns to the dashboard (#732), pinned here
		// because the e2e relies on the exact href surviving html/template
		// URL normalization.
		`href="/profile?next=/admin"`,
		// Cross-link to the public site.
		"View site",
	} {
		if !strings.Contains(snap.Body, want) {
			t.Errorf("admin top bar missing %q", want)
		}
	}

	// The admin footer is metadata only now: the build stamp + brand, no
	// log-out form.
	footer := footerFragment(t, snap.Body)
	if !strings.Contains(footer, "Top Banana!") {
		t.Error("admin footer missing the Top Banana! wordmark")
	}
	if strings.Contains(footer, `action="/logout"`) {
		t.Error("admin footer must be metadata only, but still carries the log-out form")
	}
}
