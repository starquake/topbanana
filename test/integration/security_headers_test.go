package integration_test

import (
	"net/http"
	"testing"
)

// cspExpected pins the exact CSP the server emits; it must match the value in
// internal/server/securityheaders.go. Duplicating it here keeps the
// integration assertion self-describing and catches drift on the live
// response (not just the middleware unit test).
const cspExpected = `default-src 'self'; ` +
	`script-src 'self' 'unsafe-inline' 'unsafe-eval'; ` +
	`style-src 'self' 'unsafe-inline'; ` +
	`img-src 'self'; ` +
	`font-src 'self'; ` +
	`connect-src 'self'; ` +
	`media-src 'self' blob:; ` +
	`worker-src 'self'; ` +
	`object-src 'none'; ` +
	`base-uri 'none'; ` +
	`frame-ancestors 'none'`

// assertCommonSecurityHeaders checks the headers that are present on every
// response regardless of environment. HSTS is asserted separately because it
// is gated on SecureCookies (non-development).
func assertCommonSecurityHeaders(t *testing.T, resp *http.Response) {
	t.Helper()

	want := map[string]string{
		"Content-Security-Policy": cspExpected,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
}

// assertHSTSPresent checks that Strict-Transport-Security is set (used in
// the production-env case where SecureCookies is true).
func assertHSTSPresent(t *testing.T, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("Strict-Transport-Security"); got == "" {
		t.Error("want Strict-Transport-Security header, got empty")
	}
}

// assertHSTSAbsent checks that Strict-Transport-Security is NOT set (used in
// the development-env case where a plain-HTTP dev server must not pin itself).
func assertHSTSAbsent(t *testing.T, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("want no Strict-Transport-Security header, got %q", got)
	}
}

// TestSecurityHeaders_OnEverySurface pins that the security headers land on
// every surface a visitor or player hits: the public home + auth pages, the
// player SPA shell, the embedded static assets, the PWA manifest + service
// worker, the JSON API, and the authed admin console. The integration harness
// boots APP_ENV=development, so HSTS must be absent (a dev server reachable
// over plain HTTP must not pin itself).
func TestSecurityHeaders_OnEverySurface(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL
	client := &http.Client{}

	publicCases := []struct {
		name string
		path string
	}{
		{"home", "/"},
		{"login", "/login"},
		{"player SPA", "/client/"},
		{"static CSS", "/static/css/app.css"},
		{"static vendored JS", "/static/js/vendor/alpine.min.js"},
		{"manifest", "/manifest.webmanifest"},
		{"service worker", "/sw.js"},
		{"JSON API", "/api/players/me"},
	}
	for _, tc := range publicCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := httpGet(ctx, t, client, baseURL+tc.path)
			defer closeBody(t, resp.Body)
			assertCommonSecurityHeaders(t, resp)
			assertHSTSAbsent(t, resp)
		})
	}

	// The admin console is behind RequireGameHost; the first registrant is
	// promoted to admin so an authed GET /admin reaches the page (200).
	t.Run("admin console", func(t *testing.T) {
		t.Parallel()

		adminClient := authClient(t)
		registerVerifyAndMint(
			ctx,
			t,
			adminClient,
			baseURL,
			srv.DBURI,
			"sec-headers-admin",
			"sec-headers-admin-pass-123",
		)
		resp := httpGet(ctx, t, adminClient, baseURL+"/admin")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /admin status = %d, want %d", got, want)
		}
		assertCommonSecurityHeaders(t, resp)
		assertHSTSAbsent(t, resp)
	})
}

// TestSecurityHeaders_HSTSInProduction pins that HSTS is emitted once the env
// is non-development (SecureCookies true). Boots a second server with
// APP_ENV=production and asserts Strict-Transport-Security on the public
// surfaces. Only public endpoints are checked: production cookies are Secure,
// so the harness's no-TLS http.Client cannot carry a session to /admin.
func TestSecurityHeaders_HSTSInProduction(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"APP_ENV": "production",
	})
	baseURL := srv.BaseURL
	client := &http.Client{}

	for _, path := range []string{"/", "/login", "/static/css/app.css", "/sw.js"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			resp := httpGet(ctx, t, client, baseURL+path)
			defer closeBody(t, resp.Body)
			assertCommonSecurityHeaders(t, resp)
			assertHSTSPresent(t, resp)
		})
	}
}
