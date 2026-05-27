// Package absurl computes the absolute base URL for an HTTP request.
//
// Used by template renderers that emit absolute URLs into the page
// (og:image, og:url, twitter:image - see #294). Honors the de-facto
// X-Forwarded-Proto / X-Forwarded-Host headers when set by an upstream
// reverse proxy so a TLS-terminating proxy in front of a plain
// [http.ListenAndServe] still emits https://... links.
//
// Email-link callsites in internal/auth/ MUST use cfg.BaseURL instead
// of [BaseURL]: the trusted-proxy allow-list that would make XFF safe
// to read here lands in #463, and until then a forged X-Forwarded-Host
// would let an attacker mint verify / reset links pointing at a host
// they control. The og:image use is bounded enough to ride out #463
// in place; verify-email and password-reset paths are not. See #471.
package absurl

import (
	"net/http"
	"strings"
)

// BaseURL returns scheme://host for r with no trailing slash. The scheme
// is taken from X-Forwarded-Proto when set, otherwise from r.TLS; the
// host is taken from X-Forwarded-Host when set, otherwise from r.Host.
// Both headers may carry comma-separated hop lists; we take the first
// entry (the original client-facing value).
func BaseURL(r *http.Request) string {
	scheme := "http"
	if proto := firstHeaderValue(r, "X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := firstHeaderValue(r, "X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	return scheme + "://" + host
}

// firstHeaderValue returns the first comma-separated value of the named
// header, trimmed of surrounding whitespace. Empty when the header is
// absent or empty.
func firstHeaderValue(r *http.Request, name string) string {
	raw := r.Header.Get(name)
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = raw[:i]
	}

	return strings.TrimSpace(raw)
}
