//nolint:testpackage // tests the unexported securityHeaders middleware directly
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/config"
)

// cspExpected is the exact CSP value securityHeaders must set. Pinned here so
// a drift in the policy is caught alongside the integration test that asserts
// the same value on live responses.
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

// TestSecurityHeaders_SetsAllHeaders pins that every sitewide header is
// present on a normal response, the CSP matches the expected value, and the
// wrapped handler still runs and controls the status. Uses production env so
// HSTS is in scope.
func TestSecurityHeaders_SetsAllHeaders(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{AppEnvironment: config.AppEnvironmentProduction}
	called := false
	handler := securityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if !called {
		t.Fatal("wrapped handler was not called")
	}
	if got, want := rec.Code, http.StatusNoContent; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	wantHeaders := map[string]string{
		"Content-Security-Policy":   cspExpected,
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"X-Frame-Options":           "DENY",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, want := range wantHeaders {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %q = %q, want %q", k, got, want)
		}
	}
	if got, want := rec.Header().Get("Server"), ""; got != want {
		t.Errorf("Server header = %q, want %q (absent)", got, want)
	}
}

// TestSecurityHeaders_SuppressesServer pins that the middleware suppresses the
// Server response header (a little stack-fingerprinting hardening). A Server
// value is seeded before the middleware runs so the test fails if the
// middleware stops clearing it, not just if net/http happens not to add one.
func TestSecurityHeaders_SuppressesServer(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{AppEnvironment: config.AppEnvironmentProduction}
	handler := securityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	rec.Header().Set("Server", "topbanana")
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if got, want := rec.Header().Get("Server"), ""; got != want {
		t.Errorf("Server header = %q, want %q (absent)", got, want)
	}
}

// TestSecurityHeaders_HSTSGatedOnSecureCookies pins that HSTS is only set when
// cookies are Secure (any non-development env). The integration harness boots
// APP_ENV=development, so a dev server reachable over plain HTTP must not pin
// itself with HSTS.
func TestSecurityHeaders_HSTSGatedOnSecureCookies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		env      string
		wantHSTS bool
	}{
		{name: "development omits HSTS", env: config.AppEnvironmentDefault, wantHSTS: false},
		{name: "production sets HSTS", env: config.AppEnvironmentProduction, wantHSTS: true},
		{name: "unstated env fails secure and sets HSTS", env: "staging", wantHSTS: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{AppEnvironment: tc.env}
			handler := securityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

			got := rec.Header().Get("Strict-Transport-Security")
			if tc.wantHSTS && got == "" {
				t.Errorf("AppEnvironment=%q: want an HSTS header, got empty", tc.env)
			}
			if !tc.wantHSTS && got != "" {
				t.Errorf("AppEnvironment=%q: want no HSTS header, got %q", tc.env, got)
			}
		})
	}
}

// TestSecurityHeaders_HeadersSetBeforeHandler pins that the headers are on the
// response map before the wrapped handler runs, so a handler that writes its
// own headers (e.g. a 500 from recoverPanic) still carries the security set.
func TestSecurityHeaders_HeadersSetBeforeHandler(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{AppEnvironment: config.AppEnvironmentProduction}
	var seenCSP string
	handler := securityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seenCSP = w.Header().Get("Content-Security-Policy")
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if got, want := seenCSP, cspExpected; got != want {
		t.Errorf("handler saw CSP %q, want %q (headers must be set before the handler runs)", got, want)
	}
}
