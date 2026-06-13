package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// TestEmailAdmin_UnauthenticatedRedirects pins the admin gate on the
// email diagnostics page: an anonymous visitor must be redirected to
// /login rather than reaching the status panel (#321).
func TestEmailAdmin_UnauthenticatedRedirects(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// #449: a GET to a protected route carries the original URI as
	// ?next= so the login flow can drop the visitor back on the page.
	if got, want := resp.Header.Get("Location"), "/login?next=%2Fadmin%2Femail"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestEmailAdmin_UnconfiguredShowsDisabled pins the diagnostics page
// behaviour when SMTP is not wired: the status panel renders the
// "disabled (no-op)" badge so the operator can tell at a glance that
// no mail will leave the box (#321 design decision).
func TestEmailAdmin_UnconfiguredShowsDisabled(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := string(body), "disabled (no-op)"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q, got %q", want, got)
	}
	// Status section header pins the page identity so a future
	// template refactor cannot silently render an empty diagnostics
	// stub.
	if got, want := string(body), "Email diagnostics"; !strings.Contains(got, want) {
		t.Errorf("body should contain page title %q", want)
	}
}

// TestEmailAdmin_RendersBaseURLWhenSet pins that a deploy with
// BASE_URL wired surfaces the link prefix on the diagnostics page
// (#495). The dispatchers silently no-op when BASE_URL is empty;
// surfacing it next to the SMTP wiring is how the operator
// confirms email-link rendering is live.
func TestEmailAdmin_RendersBaseURLWhenSet(t *testing.T) {
	t.Parallel()

	const baseURL = "https://quiz.example.test"
	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"BASE_URL":             baseURL,
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := string(body), "Base URL"; !strings.Contains(got, want) {
		t.Errorf("body should contain row label %q", want)
	}
	if got, want := string(body), baseURL; !strings.Contains(got, want) {
		t.Errorf("body should contain BASE_URL value %q", want)
	}
}

// TestEmailAdmin_RendersBaseURLDisabledWhenEmpty pins the no-op
// signal: a deploy that left BASE_URL unset renders the same
// "disabled (no-op)" badge the SMTP-not-configured row uses, so the
// operator can tell at a glance that no email links will be sent
// (#495). Renders regardless of whether SMTP is configured.
func TestEmailAdmin_RendersBaseURLDisabledWhenEmpty(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// The "Base URL" row label confirms the row rendered; the
	// disabled badge text is shared with the SMTP-unconfigured
	// status so it would match either way - the label is the
	// load-bearing assertion.
	if got, want := string(body), "Base URL"; !strings.Contains(got, want) {
		t.Errorf("body should contain row label %q", want)
	}
	if got, want := string(body), "disabled (no-op)"; !strings.Contains(got, want) {
		t.Errorf("body should contain disabled badge %q", want)
	}
}

// TestEmailAdmin_TestSendOnUnconfiguredRedirectsWithFlash pins the
// "email is not configured" path: an admin clicking the test-send
// button on an unconfigured deploy is 303-redirected to /admin/email
// and the rendered page carries a verbatim "not configured" banner.
// The PRG bounce is what keeps Firefox from prompting "resend this
// form?" on refresh (#321).
func TestEmailAdmin_TestSendOnUnconfiguredRedirectsWithFlash(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	// Manual redirect handling so the test can inspect the 303 and
	// then issue the follow-up GET itself (the GET is what pulls and
	// clears the flash). One client + one jar throughout.
	postClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, postClient, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	csrfToken := fetchCSRFToken(ctx, t, postClient, srv.BaseURL+"/admin/email")

	form := url.Values{}
	form.Add("to", "ops@example.test")
	form.Add("csrf_token", csrfToken)

	postReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, srv.BaseURL+"/admin/email/test", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := postClient.Do(postReq)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("POST status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/admin/email"; got != want {
		t.Errorf("POST Location = %q, want %q", got, want)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	getResp, err := postClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET err = %v, want nil", err)
	}
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := getResp.Body.Close(); cerr != nil {
		t.Errorf("GET Body.Close err = %v, want nil", cerr)
	}
	if got, want := getResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET status = %d, want %d", got, want)
	}
	// Match on the banner-only phrase: the log row renders the raw
	// sentinel ("email is not configured..."), so a substring like
	// "not configured" cannot distinguish banner from history.
	if got, want := string(body), `role="alert"`; !strings.Contains(got, want) {
		t.Errorf("GET body should contain the banner role attribute %q", want)
	}
	if got, want := string(body), "set SMTP_HOST"; !strings.Contains(got, want) {
		t.Errorf("GET body should contain the banner copy %q", want)
	}
	// The flash is one-shot: a second GET on /admin/email lands a fresh
	// page without the banner, pinning that the cookie was cleared on
	// the first read.
	second, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	secondResp, err := postClient.Do(second)
	if err != nil {
		t.Fatalf("second GET err = %v, want nil", err)
	}
	secondBody, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("ReadAll second GET err = %v, want nil", err)
	}
	if cerr := secondResp.Body.Close(); cerr != nil {
		t.Errorf("second GET Body.Close err = %v, want nil", cerr)
	}
	// The banner uses role="alert"; the log row does not. Pin the
	// flash is one-shot by checking the banner-specific copy.
	if strings.Contains(string(secondBody), "set SMTP_HOST") {
		t.Error("second GET still contains the banner; flash must be one-shot")
	}
}

// TestEmailAdmin_ConfiguredSendSucceeds pins the happy path that the
// retired admin-email e2e spec covered: with SMTP wired to a reachable
// catch-all (the integration analogue of the e2e suite's Mailpit), the
// diagnostics status reads "enabled" with no "disabled (no-op)" badge,
// a test-send 303s with a success flash naming the recipient, and that
// flash is one-shot (a second GET drops it).
func TestEmailAdmin_ConfiguredSendSucceeds(t *testing.T) {
	t.Parallel()

	smtp := startFakeSMTP(t)
	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// BASE_URL set so the Base URL row also reads "enabled"; the e2e
		// spec pinned that no "disabled (no-op)" badge appears anywhere on
		// the page when the deploy is fully wired.
		"BASE_URL":  "https://topbanana.example",
		"SMTP_HOST": smtp.host(t),
		"SMTP_PORT": smtp.port(t),
		"SMTP_FROM": "noreply@topbanana.example",
		"SMTP_TLS":  "false",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	// (a) The status panel reads "enabled" and never shows the no-op badge.
	statusBody := getEmailBody(ctx, t, client, srv.BaseURL)
	if got, want := statusBody, "enabled"; !strings.Contains(got, want) {
		t.Errorf("status body should contain %q; body=%q", want, got)
	}
	if strings.Contains(statusBody, "disabled (no-op)") {
		t.Errorf("status body should not contain the no-op badge when SMTP is wired; body=%q", statusBody)
	}

	// (b) A test-send 303s to /admin/email; the follow-up GET renders the
	// success banner naming the recipient.
	csrfToken := fetchCSRFToken(ctx, t, client, srv.BaseURL+"/admin/email")
	form := url.Values{}
	form.Add("to", "ops@example.test")
	form.Add("csrf_token", csrfToken)
	postReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, srv.BaseURL+"/admin/email/test", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := postResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := postResp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("POST status = %d, want %d", got, want)
	}
	if got, want := postResp.Header.Get("Location"), "/admin/email"; got != want {
		t.Errorf("POST Location = %q, want %q", got, want)
	}

	successBody := getEmailBody(ctx, t, client, srv.BaseURL)
	if got, want := successBody, "Test email sent to ops@example.test"; !strings.Contains(got, want) {
		t.Errorf("GET body should contain the success banner %q; body=%q", want, successBody)
	}

	// The send actually reached the wire: the catch-all accepted the RCPT.
	if got := smtp.recipientCount(); got < 1 {
		t.Errorf("fake SMTP recipient count = %d, want at least 1", got)
	}

	// (c) The success flash is one-shot: a second GET drops the banner.
	clearedBody := getEmailBody(ctx, t, client, srv.BaseURL)
	if strings.Contains(clearedBody, "Test email sent to ops@example.test") {
		t.Errorf("second GET still contains the success banner; flash must be one-shot; body=%q", clearedBody)
	}
}

// getEmailBody fetches GET /admin/email as client, asserts a 200, and
// returns the rendered HTML body.
func getEmailBody(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET /admin/email status = %d, want %d", got, want)
	}

	return string(body)
}

// TestEmailAdmin_GetOnTestRouteRedirects pins the refresh-after-send
// path: a browser landing on GET /admin/email/test after a POST
// (because the form action targets that URL) gets a 303 to
// /admin/email rather than the default 405 ServeMux would return for a
// method-mismatched route (#321).
func TestEmailAdmin_GetOnTestRouteRedirects(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email/test", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	// 303 (See Other) rather than 405 (Method Not Allowed): the refresh
	// must land somewhere usable. /admin/email loses the inline banner;
	// that trade-off is documented in HandleEmailTestRefresh.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d (not %d which would be the default mux behaviour)",
			got, want, http.StatusMethodNotAllowed)
	}
	if got, want := resp.Header.Get("Location"), "/admin/email"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestEmailAdmin_RateLimitsRepeatedSends pins the per-IP cool-down on
// the test-send endpoint (#321): two clicks in quick succession from
// the same client both 303 (PRG keeps refresh-safe), but the second
// 303 carries a "Slow down" flash that the follow-up GET renders.
func TestEmailAdmin_RateLimitsRepeatedSends(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	postOnce := func() *http.Response {
		t.Helper()
		csrfToken := fetchCSRFToken(ctx, t, client, srv.BaseURL+"/admin/email")
		form := url.Values{}
		form.Add("to", "ops@example.test")
		form.Add("csrf_token", csrfToken)
		req, reqErr := http.NewRequestWithContext(
			ctx, http.MethodPost, srv.BaseURL+"/admin/email/test", strings.NewReader(form.Encode()),
		)
		if reqErr != nil {
			t.Fatalf("NewRequest err = %v, want nil", reqErr)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, postErr := client.Do(req)
		if postErr != nil {
			t.Fatalf("client.Do err = %v, want nil", postErr)
		}

		return resp
	}

	first := postOnce()
	if _, drainErr := io.Copy(io.Discard, first.Body); drainErr != nil {
		t.Errorf("drain first body err = %v, want nil", drainErr)
	}
	if cerr := first.Body.Close(); cerr != nil {
		t.Errorf("first Body.Close err = %v, want nil", cerr)
	}
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("first POST status = %d, want %d (PRG must redirect even on the admit path)", got, want)
	}

	second := postOnce()
	if _, drainErr := io.Copy(io.Discard, second.Body); drainErr != nil {
		t.Errorf("drain second body err = %v, want nil", drainErr)
	}
	if cerr := second.Body.Close(); cerr != nil {
		t.Errorf("second Body.Close err = %v, want nil", cerr)
	}
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second POST status = %d, want %d", got, want)
	}
	if got, want := second.Header.Get("Location"), "/admin/email"; got != want {
		t.Errorf("second POST Location = %q, want %q", got, want)
	}

	// Pull the rate-limit banner off the follow-up GET, which is where
	// the user actually sees the message.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/email", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET err = %v, want nil", err)
	}
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := getResp.Body.Close(); cerr != nil {
		t.Errorf("GET Body.Close err = %v, want nil", cerr)
	}
	if got, want := string(body), "Slow down"; !strings.Contains(got, want) {
		t.Errorf("GET body should contain rate-limit banner %q", want)
	}
}
