//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestAdminPlayerMgmt_FilterTabsAndCounts pins the slice 2 ?state=
// filter wiring end-to-end: registering one verified admin + one
// unverified player should surface counts on both tabs, and filtering
// to ?state=unverified should hide the admin row.
func TestAdminPlayerMgmt_FilterTabsAndCounts(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "mgmt-admin", "mgmt-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "mgmt-unverified", "mgmt-unverified-pass-123")

	body := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players")
	if got, want := body, "Unverified"; !strings.Contains(got, want) {
		t.Errorf("body should contain tab %q; body=%q", want, body)
	}
	if got, want := body, "mgmt-unverified"; !strings.Contains(got, want) {
		t.Errorf("body should contain unverified row; body=%q", want)
	}

	filtered := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players?state=unverified")
	if got, want := filtered, "mgmt-unverified"; !strings.Contains(got, want) {
		t.Errorf("filtered body should contain unverified row; body=%q", want)
	}
	// The admin's email is mgmt-admin@example.test so the substring
	// can still appear in the navbar's "Signed in as" footer; match
	// the row-specific link target instead.
	adminID := lookupPlayerID(ctx, t, srv.DBURI, "mgmt-admin")
	// Quote-agnostic match so a future template tweak from " to '
	// on href values doesn't silently turn this assertion into a no-op.
	adminLinkRE := regexp.MustCompile(`href=["']/admin/players/` + intToString(adminID) + `["']`)
	if adminLinkRE.MatchString(filtered) {
		t.Errorf("filtered body should not contain admin row link for id=%d", adminID)
	}
}

// TestAdminPlayerMgmt_DetailViewRenders pins slice 3: an admin GET on
// the per-player detail route surfaces the row + the action buttons.
func TestAdminPlayerMgmt_DetailViewRenders(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "detail-admin", "detail-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "detail-target", "detail-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "detail-target")

	body := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	for _, want := range []string{"detail-target", "Mark verified", "Resend verification", "Set / overwrite email"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body should contain %q; body=%q", want, body)
		}
	}
}

// TestAdminPlayerMgmt_MarkVerifiedFlipsRow drives the slice 4 verify
// action end-to-end: an unverified target stamped via the action lands
// in the verified bucket, the row appears in the audit trail, and the
// player can log in afterwards.
func TestAdminPlayerMgmt_MarkVerifiedFlipsRow(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "verify-admin", "verify-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "verify-target", "verify-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "verify-target")
	verifyURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/verify"

	postAdminAction(ctx, t, adminClient, srv.BaseURL, verifyURL, nil)

	body := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players?state=verified")
	if got, want := body, "verify-target"; !strings.Contains(got, want) {
		t.Errorf("verified-tab body should contain target after mark; body=%q", body)
	}

	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "Marked verified"; !strings.Contains(got, want) {
		t.Errorf("audit trail should record action; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_WrongStateRejected pins the slice 4 guard: a
// verified row cannot be re-marked-verified (the state was wrong, so
// the action does not run and the operator sees the banner).
func TestAdminPlayerMgmt_WrongStateRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "reject-admin", "reject-admin-pass-123")

	// Try to mark the admin (already verified) verified again - should
	// be rejected because the row is not in the unverified bucket.
	adminID := lookupPlayerID(ctx, t, srv.DBURI, "reject-admin")
	verifyURL := srv.BaseURL + "/admin/players/" + intToString(adminID) + "/verify"

	res := postAdminAction(ctx, t, adminClient, srv.BaseURL, verifyURL, nil)
	if got, want := res.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (wrong-state still PRGs)", got, want)
	}

	// Follow the 303 to see the banner.
	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(adminID),
	)
	if got, want := detail, "not in the"; !strings.Contains(got, want) {
		t.Errorf("body should contain reject banner; body=%q", detail)
	}
	if got, want := detail, "state"; !strings.Contains(got, want) {
		t.Errorf("body should contain reject banner; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_SetEmailValidates pins the slice 4 SetEmail
// validation: an empty / malformed address re-renders the page with
// the error banner.
func TestAdminPlayerMgmt_SetEmailValidates(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "setemail-admin", "setemail-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "setemail-target", "setemail-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "setemail-target")
	emailURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/email"

	postAdminAction(ctx, t, adminClient, srv.BaseURL, emailURL, url.Values{"email": {"not-an-email"}})
	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "valid email address"; !strings.Contains(got, want) {
		t.Errorf("body should contain validation banner; body=%q", detail)
	}

	// A good address now succeeds.
	postAdminAction(
		ctx, t, adminClient, srv.BaseURL, emailURL,
		url.Values{"email": {"setemail-new@example.test"}},
	)
	detail = getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "setemail-new@example.test"; !strings.Contains(got, want) {
		t.Errorf("body should contain new email; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_SetEmailClearsVerification pins fix #450 follow-up:
// changing the email of an already-verified player clears verification so
// the new, unproven address must be re-verified. The target moves out of
// the verified bucket into unverified.
func TestAdminPlayerMgmt_SetEmailClearsVerification(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "reverify-admin", "reverify-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "reverify-target", "reverify-target-pass-123")
	// Verify the target so it starts in the verified bucket.
	verifyPlayerEmail(ctx, t, srv.DBURI, "reverify-target")

	target := lookupPlayerID(ctx, t, srv.DBURI, "reverify-target")
	// Match on the role-scoped detail link rather than an email/username
	// substring so a flash/banner echoing the value elsewhere on the page
	// cannot satisfy the assertion (same hazard documented in
	// TestAdminPlayerMgmt_FilterTabsAndCounts).
	targetLinkRE := regexp.MustCompile(`href=["']/admin/players/` + intToString(target) + `["']`)
	verified := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players?state=verified")
	if !targetLinkRE.MatchString(verified) {
		t.Fatalf("verified-tab body should contain target link before email change; body=%q", verified)
	}

	emailURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/email"
	postAdminAction(
		ctx, t, adminClient, srv.BaseURL, emailURL,
		url.Values{"email": {"reverify-new@example.test"}},
	)

	// After the change the target is in the unverified bucket, not the
	// verified one.
	unverified := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players?state=unverified")
	if !targetLinkRE.MatchString(unverified) {
		t.Errorf("unverified-tab body should contain target link after email change; body=%q", unverified)
	}

	verifiedAfter := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players?state=verified")
	if targetLinkRE.MatchString(verifiedAfter) {
		t.Errorf("verified-tab should not contain target link after email change; body=%q", verifiedAfter)
	}
}

// TestAdminPlayerMgmt_CreatePlayer pins the "create without
// verification" flow: an admin POSTs to /admin/players, the resulting
// row is verified, and the new player can log in immediately.
func TestAdminPlayerMgmt_CreatePlayer(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "create-admin", "create-admin-pass-123")

	postAdminAction(
		ctx, t, adminClient, srv.BaseURL,
		srv.BaseURL+"/admin/players",
		url.Values{
			"display_name": {"create-target"},
			"email":        {"create-target@example.test"},
			"password":     {"create-target-pass-123"},
		},
	)

	// The newly-created player can log in even though they never went
	// through the email loop - that is the whole point of this action.
	newClient := newAdminMgmtClient(t)
	if got := loginForRedirect(
		ctx, t, newClient, srv.BaseURL,
		"create-target", "create-target-pass-123",
	); got != "/" {
		t.Errorf("new player Login redirect = %q, want %q", got, "/")
	}

	// Audit trail records the create.
	target := lookupPlayerID(ctx, t, srv.DBURI, "create-target")
	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "Created"; !strings.Contains(got, want) {
		t.Errorf("audit trail should record create; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_CreateRequiresAdmin pins that direct account creation is
// Admin-only (#538): a Host gets a 404 (not a 403) on both the GET form and the
// POST submit, so the route's existence stays hidden. The POST carries a valid
// CSRF token scraped off the Path=/ cookie via /admin so the 404 comes from the
// admin gate, not the CSRF middleware.
//
// The first credentialled registrant is auto-promoted to Admin, so the Host
// under test is a *later* registrant demoted off that promotion to Host.
func TestAdminPlayerMgmt_CreateRequiresAdmin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(
		ctx,
		t,
		adminClient,
		srv.BaseURL,
		srv.DBURI,
		"create-admin-boss",
		"create-admin-boss-pass-123",
	)

	hostClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, hostClient, srv.BaseURL, srv.DBURI, "create-host", "create-host-pass-123")
	makeHost(ctx, t, srv.DBURI, "create-host")

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, srv.BaseURL+"/admin/players/new", nil,
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := hostClient.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("GET /admin/players/new status = %d, want %d", got, want)
	}

	if got, want := postCSRFForm(
		ctx, t, hostClient, srv.BaseURL+"/admin/players",
	), http.StatusNotFound; got != want {
		t.Errorf("POST /admin/players status = %d, want %d", got, want)
	}
}

// TestAdminPlayerMgmt_RequiresCSRF pins the CSRF gate: a POST without
// the matching token is rejected with 403 by the middleware. The
// no-CSRF case is the bottom of the request stack; the action itself
// never runs.
func TestAdminPlayerMgmt_RequiresCSRF(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "csrf-admin", "csrf-admin-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "csrf-admin")
	verifyURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/verify"
	form := url.Values{} // no csrf_token

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := adminClient.Do(req)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v", cerr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusForbidden; got != want {
		t.Errorf("no-CSRF status = %d, want %d", got, want)
	}
}

// TestAdminPlayerMgmt_NonAdminBlocked pins the requireAdmin gate on the
// player-management routes: a signed-in non-Admin gets a plain 404 (the
// route's existence stays hidden, #538) and never reaches the page.
func TestAdminPlayerMgmt_NonAdminBlocked(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "nonadmin-admin", "nonadmin-admin-pass-123")

	playerClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, playerClient, srv.BaseURL, srv.DBURI, "nonadmin-target", "nonadmin-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "nonadmin-target")
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		srv.BaseURL+"/admin/players/"+intToString(target), nil,
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := playerClient.Do(req)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v", cerr)
		}
	}()
	// RequireAdmin (auth/middleware.go) 303s an anonymous request to /login
	// and returns a plain 404 for a signed-in non-Admin so the route's
	// existence stays hidden (#538). The probe is a signed-in Player, so it
	// is the 404 path.
	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("non-admin GET status = %d, want %d", got, want)
	}
}

// TestAdminPlayerMgmt_SetRolePromotesToAdmin drives the #538 id-based
// role endpoint from an Admin: a plain player is promoted to admin, the row
// reflects the new role, and the audit trail records the role_changed action
// on the detail page.
func TestAdminPlayerMgmt_SetRolePromotesToAdmin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "role-admin", "role-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "role-target", "role-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "role-target")
	roleURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/role"

	res := postAdminAction(ctx, t, adminClient, srv.BaseURL, roleURL, url.Values{"role": {"admin"}})
	if got, want := res.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("set-role status = %d, want %d", got, want)
	}

	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "Role changed"; !strings.Contains(got, want) {
		t.Errorf("audit trail should record the role change; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_SetUsername drives the #535 username endpoint from a
// super admin: the target's display name is rewritten, the new name shows on
// the detail page, and the audit trail records the "Display name set" action.
func TestAdminPlayerMgmt_SetUsername(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "setname-admin", "setname-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "setname-target", "setname-target-pass-123")

	target := lookupPlayerID(ctx, t, srv.DBURI, "setname-target")
	usernameURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/username"

	res := postAdminAction(
		ctx,
		t,
		adminClient,
		srv.BaseURL,
		usernameURL,
		url.Values{"display_name": {"renamed-target"}},
	)
	if got, want := res.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("set-username status = %d, want %d", got, want)
	}

	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "renamed-target"; !strings.Contains(got, want) {
		t.Errorf("detail body should contain the new name; body=%q", detail)
	}
	if got, want := detail, "Display name set"; !strings.Contains(got, want) {
		t.Errorf("audit trail should record the rename; body=%q", detail)
	}
}

// TestAdminPlayerMgmt_SetPassword drives the #535 password endpoint from a
// super admin: the reset 303s back to the detail page, the audit trail
// records "Password set", and the target can then log in with the new
// password.
func TestAdminPlayerMgmt_SetPassword(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "setpass-admin", "setpass-admin-pass-123")

	registerForPending(ctx, t, newAdminMgmtClient(t), srv.BaseURL, "setpass-target", "setpass-target-pass-123")
	verifyPlayerEmail(ctx, t, srv.DBURI, "setpass-target")

	target := lookupPlayerID(ctx, t, srv.DBURI, "setpass-target")
	passwordURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/password"

	res := postAdminAction(
		ctx, t, adminClient, srv.BaseURL, passwordURL,
		url.Values{"password": {"setpass-new-pass-123"}},
	)
	if got, want := res.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("set-password status = %d, want %d", got, want)
	}

	detail := getOK(
		ctx, t, adminClient,
		srv.BaseURL+"/admin/players/"+intToString(target),
	)
	if got, want := detail, "Password set"; !strings.Contains(got, want) {
		t.Errorf("audit trail should record the password reset; body=%q", detail)
	}

	// The target can log in with the new password.
	newClient := newAdminMgmtClient(t)
	if got := loginForRedirect(
		ctx, t, newClient, srv.BaseURL,
		"setpass-target", "setpass-new-pass-123",
	); got != "/" {
		t.Errorf("login with new password redirect = %q, want %q", got, "/")
	}
}

// TestAdminPlayerMgmt_SetCredentialsRequireAdmin pins that the #535 username +
// password endpoints are Admin-only (#538): a Host gets a 404 (not a 403) on
// both POSTs so the routes' existence stays hidden. The first registrant is
// auto-promoted to Admin, so the Host under test is a later registrant demoted
// off that promotion.
func TestAdminPlayerMgmt_SetCredentialsRequireAdmin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	adminClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, adminClient, srv.BaseURL, srv.DBURI, "creds-admin-boss", "creds-admin-boss-pass-123")

	hostClient := newAdminMgmtClient(t)
	registerVerifyAndMint(ctx, t, hostClient, srv.BaseURL, srv.DBURI, "creds-host", "creds-host-pass-123")
	makeHost(ctx, t, srv.DBURI, "creds-host")

	target := lookupPlayerID(ctx, t, srv.DBURI, "creds-admin-boss")
	usernameURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/username"
	passwordURL := srv.BaseURL + "/admin/players/" + intToString(target) + "/password"

	if got, want := postCSRFForm(
		ctx, t, hostClient, usernameURL,
	), http.StatusNotFound; got != want {
		t.Errorf("POST /username status = %d, want %d", got, want)
	}
	if got, want := postCSRFForm(
		ctx, t, hostClient, passwordURL,
	), http.StatusNotFound; got != want {
		t.Errorf("POST /password status = %d, want %d", got, want)
	}
}

// newAdminMgmtClient is the per-test client builder used by every
// /admin/players/* probe in this file. Wraps authClient with the
// same don't-follow-redirects policy so the test can assert on the
// 303 Location directly.
func newAdminMgmtClient(t *testing.T) *http.Client {
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

// lookupPlayerID resolves the players.id for the given username via a
// direct DB read. Used so the per-player URL can be built without
// scraping the admin list page.
func lookupPlayerID(ctx context.Context, t *testing.T, dbURI, username string) int64 {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.
	player, err := stores.Players.GetPlayerByUsername(ctx, username)
	if err != nil {
		t.Fatalf("lookupPlayerID err = %v, want nil", err)
	}

	return player.ID
}

// adminActionResult is what postAdminAction returns to callers. We
// surface a small projection of the response instead of *http.Response
// so the body close (which we always do inside the helper) never
// becomes the caller's problem - the bodyclose linter otherwise flags
// every probe site for forgetting to close.
type adminActionResult struct {
	StatusCode int
	Location   string
}

// postAdminAction is the shared form-POST helper for the /admin/players
// action probes. The CSRF nonce cookie + matching token are scraped off
// the per-target page that hosts the form: per-player POSTs read the
// token from the detail view; the create POST reads it from the new
// form.
func postAdminAction(
	ctx context.Context, t *testing.T, client *http.Client,
	baseURL, postURL string, extra url.Values,
) adminActionResult {
	t.Helper()
	csrfURL := csrfPageForPostURL(baseURL, postURL)
	token := fetchCSRFToken(ctx, t, client, csrfURL)

	form := url.Values{}
	for k, vals := range extra {
		for _, v := range vals {
			form.Add(k, v)
		}
	}
	form.Add("csrf_token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := drainAndClose(resp.Body); cerr != nil {
			t.Errorf("drain err = %v", cerr)
		}
	}()

	return adminActionResult{
		StatusCode: resp.StatusCode,
		Location:   resp.Header.Get("Location"),
	}
}

// csrfPageForPostURL returns the GET URL whose body carries the CSRF
// token + sets the matching nonce cookie for the supplied POST URL.
// Per-target POSTs (verify, resend, email) read off the detail view;
// the create POST reads off /admin/players/new.
func csrfPageForPostURL(baseURL, postURL string) string {
	if strings.HasSuffix(postURL, "/admin/players") {
		return baseURL + "/admin/players/new"
	}
	for _, suffix := range []string{
		"/verify", "/resend-verification", "/email", "/role", "/username", "/password",
	} {
		if before, ok := strings.CutSuffix(postURL, suffix); ok {
			return before
		}
	}

	return postURL
}

// drainAndClose reads the body to completion and closes it so the
// connection can be returned to the pool. Required for keep-alive
// reuse across the multi-POST probes.
func drainAndClose(body io.ReadCloser) error {
	_, _ = io.Copy(io.Discard, body)

	return body.Close()
}

// intToString is a thin alias for strconv.FormatInt that keeps the
// per-player URL-building call sites readable.
func intToString(n int64) string {
	return strconv.FormatInt(n, 10)
}
