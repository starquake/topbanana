// Package absurl computes the absolute base URL for an HTTP request.
//
// The configured BASE_URL wins when set. Otherwise the URL is built from the
// request, honouring X-Forwarded-Proto / X-Forwarded-Host only when the request
// came straight from a trusted proxy (TRUSTED_PROXY_CIDRS), so a client cannot
// pick the host that og:image or the host join-QR point at (#471, #1355).
package absurl

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/request"
)

type ctxKey struct{}

// Middleware resolves each request's base URL from configured (the BASE_URL
// setting, may be empty) and trustedCIDRs, and stores it for [BaseURL].
func Middleware(configured string, trustedCIDRs []*net.IPNet) func(http.Handler) http.Handler {
	configured = strings.TrimRight(configured, "/")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			base := resolve(r, configured, trustedCIDRs)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, base)))
		})
	}
}

// BaseURL returns scheme://host for r with no trailing slash, as resolved by
// [Middleware]. Without the middleware it trusts no forwarded headers and
// builds the URL from r.TLS and r.Host alone.
func BaseURL(r *http.Request) string {
	if base, ok := r.Context().Value(ctxKey{}).(string); ok {
		return base
	}

	return resolve(r, "", nil)
}

// resolve picks configured when set; otherwise it builds the URL from the
// request, taking X-Forwarded-Proto / X-Forwarded-Host only from a trusted
// proxy and only its own (rightmost) hop, since a proxy that appends leaves any
// client-sent value first.
func resolve(r *http.Request, configured string, trustedCIDRs []*net.IPNet) string {
	if configured != "" {
		return configured
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if request.FromTrustedProxy(r, trustedCIDRs) {
		if proto := lastHeaderValue(r, "X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}
		if fwdHost := lastHeaderValue(r, "X-Forwarded-Host"); fwdHost != "" {
			host = fwdHost
		}
	}

	return scheme + "://" + host
}

// lastHeaderValue returns the last comma-separated value across every line of
// the named header, trimmed. Empty when the header is absent or empty.
func lastHeaderValue(r *http.Request, name string) string {
	raw := strings.Join(r.Header.Values(name), ",")
	if i := strings.LastIndexByte(raw, ','); i >= 0 {
		raw = raw[i+1:]
	}

	return strings.TrimSpace(raw)
}
