package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// brandLink is the brand link's aria-label. Its accessible name "Top Banana!"
// is what the e2e role locators key on, so the integration tests pin the
// exact attribute. Used by both the admin topbar (which still has one) and
// the client footer (which carries the brand now that the topbar is gone).
const brandLink = `aria-label="Top Banana!"`

// footerFragment returns the metadata footer slice of a rendered page so
// the footer-only assertions do not accidentally match controls a top bar
// carries. The site footer is the last <footer> on the page.
func footerFragment(t *testing.T, body string) string {
	t.Helper()

	idx := strings.LastIndex(body, "<footer")
	if idx == -1 {
		t.Fatalf("page has no <footer> (body excerpt: %.200q)", body)
	}

	return body[idx:]
}

// bodyBeforeFooter returns the page slice that precedes the last <footer>.
// Useful for asserting that the client surfaces (#893) carry no topbar
// above the content — the only "Primary" nav / brand / account cluster
// should live inside the footer.
func bodyBeforeFooter(t *testing.T, body string) string {
	t.Helper()

	idx := strings.LastIndex(body, "<footer")
	if idx == -1 {
		t.Fatalf("page has no <footer> (body excerpt: %.200q)", body)
	}

	return body[:idx]
}

// TestClientChrome_HomeAnonymous pins the client footer on the anonymous
// home page (#893): the brand wordmark, a Log in link in the account
// cluster, and the Manage cross-link to /admin all live in the FOOTER
// instead of a top bar. The page above the footer carries no topbar.
func TestClientChrome_HomeAnonymous(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	body := getBody(ctx, t, srv.BaseURL+"/")

	// The client surface intentionally drops the shared topbar.
	above := bodyBeforeFooter(t, body)
	for _, banned := range []string{
		`aria-label="Primary"`,
		brandLink,
	} {
		if strings.Contains(above, banned) {
			t.Errorf("client home should not carry a topbar; found %q above the footer", banned)
		}
	}

	footer := footerFragment(t, body)
	for _, want := range []string{
		brandLink,
		`href="/login"`,
		`href="/admin"`,
		"Manage quizzes",
	} {
		if !strings.Contains(footer, want) {
			t.Errorf("client home footer missing %q", want)
		}
	}
	if strings.Contains(footer, "Signed in as") {
		t.Error("anonymous home footer should not render a signed-in identity")
	}
	if strings.Contains(footer, `action="/logout"`) {
		t.Error("anonymous home footer should not render a log-out form")
	}
}

// TestClientChrome_HomeSignedIn pins the signed-in account cluster in the
// home client footer: the viewer's display name links to /profile, a
// log-out form POSTs to /logout, and the anonymous Log in link is gone.
func TestClientChrome_HomeSignedIn(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerAndMint(ctx, t, client, srv.BaseURL, srv.DBURI, "client-home-user", "correct-battery-13")

	snap := fetchWithClient(ctx, t, client, srv.BaseURL+"/")
	if got, want := snap.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	above := bodyBeforeFooter(t, snap.Body)
	if strings.Contains(above, `aria-label="Primary"`) {
		t.Error("client home should not carry a topbar above the footer")
	}

	footer := footerFragment(t, snap.Body)
	for _, want := range []string{
		brandLink,
		"Signed in as",
		"client-home-user",
		`href="/profile"`,
		`action="/logout"`,
		"Manage quizzes",
	} {
		if !strings.Contains(footer, want) {
			t.Errorf("signed-in client home footer missing %q", want)
		}
	}
	if strings.Contains(footer, `href="/login"`) {
		t.Error("signed-in client home footer should not render the anonymous Log in link")
	}
}

// TestClientChrome_LoginPage pins the client footer on an auth surface
// (the login page): the viewer is anonymous, so the account cluster shows
// Log in and the cross-link shows Manage. The brand wordmark in the
// footer points home.
func TestClientChrome_LoginPage(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	body := getBody(ctx, t, srv.BaseURL+"/login")

	above := bodyBeforeFooter(t, body)
	if strings.Contains(above, `aria-label="Primary"`) {
		t.Error("login page should not carry a topbar above the footer")
	}

	footer := footerFragment(t, body)
	for _, want := range []string{
		brandLink,
		`href="/admin"`,
		"Manage quizzes",
	} {
		if !strings.Contains(footer, want) {
			t.Errorf("login footer missing %q", want)
		}
	}
	if !strings.Contains(footer, `href="/" aria-label="Top Banana!"`) {
		t.Error("login footer brand link should point at /")
	}
	if strings.Contains(footer, "Signed in as") {
		t.Error("anonymous login footer should not render a signed-in identity")
	}
}

// TestTopbar_AdminConsole pins the shared top bar on an admin page: the
// brand link points at /admin (logoHref), the section nav is present, the
// account cluster shows the signed-in admin plus a log-out form, and the
// cross-link is "View site" -> /. The admin keeps the topbar (#893 only
// drops it on client surfaces); the admin footer stays metadata-only.
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

// TestTopbar_AdminSectionsReachableAndActive pins the persistent admin
// top nav (#517 / #582): the navbar links every real section (Quizzes,
// Players, Invites, Email - Email was orphaned before #517 and Invites
// before #582), each section page loads under its own heading, and the
// active section carries aria-current="page" while the others do not.
func TestTopbar_AdminSectionsReachableAndActive(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "sections-admin", "correct-battery-13")

	// The nav links all four admin sections from any admin page.
	quizzes := fetchWithClient(ctx, t, client, srv.BaseURL+"/admin/quizzes")
	if got, want := quizzes.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	for _, want := range []string{
		`href="/admin/quizzes"`,
		`href="/admin/players"`,
		`href="/admin/invites"`,
		`href="/admin/email"`,
	} {
		if !strings.Contains(quizzes.Body, want) {
			t.Errorf("admin top nav missing section link %q", want)
		}
	}

	// Each section page loads (200) under its own <h1> heading. Match the
	// closing >...</h1> form so the assertion pins the page heading and
	// cannot be satisfied by the same word appearing as a nav-link label
	// (the nav renders Players/Invites on every admin page).
	sections := []struct {
		path    string
		heading string
	}{
		{"/admin/players", ">Players</h1>"},
		{"/admin/invites", ">Invites</h1>"},
		{"/admin/email", ">Email diagnostics</h1>"},
	}
	for _, s := range sections {
		snap := fetchWithClient(ctx, t, client, srv.BaseURL+s.path)
		if got, want := snap.StatusCode, http.StatusOK; got != want {
			t.Errorf("GET %s status = %d, want %d", s.path, got, want)
		}
		if !strings.Contains(snap.Body, s.heading) {
			t.Errorf("GET %s body missing heading %q", s.path, s.heading)
		}
	}

	// The active section's nav link carries aria-current="page"; the
	// others do not. The topbar renders the attribute inline next to the
	// section's own href (see components/topbar.gohtml).
	players := fetchWithClient(ctx, t, client, srv.BaseURL+"/admin/players")
	if !navLinkIsActive(players.Body, "/admin/players") {
		t.Error(`on /admin/players the Players nav link should carry aria-current="page"`)
	}
	if navLinkIsActive(players.Body, "/admin/quizzes") {
		t.Error(`on /admin/players the Quizzes nav link must not carry aria-current="page"`)
	}

	invites := fetchWithClient(ctx, t, client, srv.BaseURL+"/admin/invites")
	if !navLinkIsActive(invites.Body, "/admin/invites") {
		t.Error(`on /admin/invites the Invites nav link should carry aria-current="page"`)
	}
	if navLinkIsActive(invites.Body, "/admin/players") {
		t.Error(`on /admin/invites the Players nav link must not carry aria-current="page"`)
	}
}

// TestTopbar_HostGatedFromAdminSections pins that the Admin-only sections
// stay hidden from a Host (#538): the rendered /admin/quizzes page carries
// only the Quizzes link, never the Players / Email / Settings links the
// isAdmin block gates, and a direct GET to /admin/players or /admin/email
// is a 404 (the routes' existence stays hidden, not a 403). The first
// credentialled registrant is auto-promoted to Admin, so the Host under
// test is a later registrant demoted off that promotion.
func TestTopbar_HostGatedFromAdminSections(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	// Consume the first-registrant Admin promotion with a boss, then a
	// later registrant demoted to Host is the gate's subject.
	registerAdminClient(ctx, t, srv.BaseURL, srv.DBURI, "topbar-host-boss")
	host := registerAdminClient(ctx, t, srv.BaseURL, srv.DBURI, "topbar-host")
	makeHost(ctx, t, srv.DBURI, "topbar-host")

	// A Host still reaches the console and the Quizzes section.
	quizzes := fetchWithClient(ctx, t, host, srv.BaseURL+"/admin/quizzes")
	if got, want := quizzes.StatusCode, http.StatusOK; got != want {
		t.Fatalf("host GET /admin/quizzes status = %d, want %d", got, want)
	}
	if !strings.Contains(quizzes.Body, `href="/admin/quizzes"`) {
		t.Error("host admin nav should still carry the Quizzes link")
	}
	// The Admin-only section links are absent from the rendered nav.
	for _, banned := range []string{
		`href="/admin/players"`,
		`href="/admin/email"`,
		`href="/admin/settings"`,
	} {
		if strings.Contains(quizzes.Body, banned) {
			t.Errorf("host admin nav must not carry the Admin-only link %q", banned)
		}
	}

	// The routes themselves stay hidden: a direct hit is a 404, not a 403.
	for _, path := range []string{"/admin/players", "/admin/email"} {
		snap := fetchWithClient(ctx, t, host, srv.BaseURL+path)
		if got, want := snap.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host GET %s status = %d, want %d", path, got, want)
		}
	}
}

// navLinkIsActive reports whether the nav link to href in body carries
// aria-current="page". The topbar renders the attribute inline right
// after the href (`<a href="/admin/players" aria-current="page" ...`),
// so a link is active iff aria-current appears before the next tag close
// following its href.
func navLinkIsActive(body, href string) bool {
	needle := `href="` + href + `"`
	_, afterHref, ok := strings.Cut(body, needle)
	if !ok {
		return false
	}
	tagTail, _, ok := strings.Cut(afterHref, ">")
	if !ok {
		return false
	}

	return strings.Contains(tagTail, `aria-current="page"`)
}
