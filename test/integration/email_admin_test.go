//go:build integration

package integration_test

import (
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
	if got, want := resp.Header.Get("Location"), "/login"; got != want {
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

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

// TestEmailAdmin_TestSendOnUnconfiguredReturns503 pins the "email is
// not configured" path: an admin clicking the test-send button on an
// unconfigured deploy gets a clear banner + 503 rather than a 500 or
// a silent success (#321).
func TestEmailAdmin_TestSendOnUnconfiguredReturns503(t *testing.T) {
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

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

	resp, err := client.Do(postReq)
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

	if got, want := resp.StatusCode, http.StatusServiceUnavailable; got != want {
		t.Errorf("status = %d, want %d (body=%q)", got, want, body)
	}
	if got, want := string(body), "not configured"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q, got %q", want, got)
	}
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

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
// the same client return 429 + Retry-After on the second request,
// even when the mailer is unconfigured (so the rate limit applies
// before the SMTP layer would refuse).
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

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
	if got := first.StatusCode; got == http.StatusTooManyRequests {
		t.Fatal("first POST already 429 - rate limit applied before any send")
	}

	second := postOnce()
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := second.Body.Close(); cerr != nil {
		t.Errorf("second Body.Close err = %v, want nil", cerr)
	}
	if got, want := second.StatusCode, http.StatusTooManyRequests; got != want {
		t.Errorf("second POST status = %d, want %d (body=%q)", got, want, body)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Errorf("Retry-After = %q, want non-empty", got)
	}
}
