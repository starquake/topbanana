package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/config"
)

// contentSecurityPolicy is the sitewide CSP. It starts loose: script-src
// allows 'unsafe-inline' (inline onclick/onsubmit handlers in admin templates,
// inline SW-registration scripts, and Alpine attribute expressions) and
// 'unsafe-eval' (Alpine.js v3 evaluates x-data/x-show/@click expressions via
// new Function, which CSP gates on 'unsafe-eval' not 'unsafe-inline').
// style-src allows 'unsafe-inline' because of two inline style attributes.
// Tightening to a nonce-based strict CSP is a follow-up tracked separately.
const contentSecurityPolicy = `default-src 'self'; ` +
	`script-src 'self' 'unsafe-inline' 'unsafe-eval'; ` +
	`style-src 'self' 'unsafe-inline'; ` +
	`img-src 'self'; ` +
	`font-src 'self'; ` +
	`connect-src 'self'; ` +
	`media-src 'self'; ` +
	`worker-src 'self'; ` +
	`object-src 'none'; ` +
	`base-uri 'none'; ` +
	`frame-ancestors 'none'`

// strictTransportSecurity is the HSTS value applied only when cookies are
// Secure (any non-development env). Browsers ignore HSTS over HTTP, so gating
// avoids pinning a dev laptop that serves plain HTTP.
const strictTransportSecurity = "max-age=31536000; includeSubDomains"

// securityHeaders sets the sitewide security response headers. Wire it as the
// innermost wrapper so the headers are on w.Header() before any handler writes
// the response, including recoverPanic's 500 on a handler panic (the headers
// stay on the header map across the unwind). HSTS is gated on SecureCookies so
// a development server reachable over plain HTTP does not pin itself.
func securityHeaders(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			if cfg.SecureCookies() {
				h.Set("Strict-Transport-Security", strictTransportSecurity)
			}
			next.ServeHTTP(w, r)
		})
	}
}
