// Package absurl computes the absolute base URL for an HTTP request, honoring
// the X-Forwarded-Proto / X-Forwarded-Host headers from an upstream proxy.
//
// Security contract: [BaseURL] trusts those headers, so it is only safe for
// og:image / og:url social-card metadata (#294), where a forged
// X-Forwarded-Host at worst points a preview image at the wrong host.
// Navigation targets (email links in internal/auth, the host join-QR in
// internal/host) MUST use cfg.BaseURL instead, or a forged header could
// redirect a real user to an attacker host. The trusted-proxy allow-list that
// would make XFF safe here lands in #463. See #471.
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
