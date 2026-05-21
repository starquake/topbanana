// Package absurl computes the absolute base URL for an HTTP request.
//
// Used by template renderers that emit absolute URLs into the page
// (og:image, og:url, twitter:image — see #294) and intended to back the
// email-link rendering that lands in #290 too. Honors the de-facto
// X-Forwarded-Proto / X-Forwarded-Host headers when set by an upstream
// reverse proxy so a TLS-terminating proxy in front of a plain
// [http.ListenAndServe] still emits https://... links.
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
