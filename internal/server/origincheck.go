package server

import (
	"net/http"
	"net/url"
	"strings"
)

// sameOriginCheck wraps the JSON API handler chain with a same-origin guard on
// unsafe HTTP methods (POST, PUT, PATCH, DELETE) as CSRF defence-in-depth on
// top of the session cookie's SameSite=Lax. Safe methods (GET, HEAD, OPTIONS)
// pass through untouched so the SSE / polling reads are never blocked.
//
// expectedOrigin is the server's own scheme://host (derived from cfg.BaseURL
// via originFromBaseURL). When it is empty - BaseURL unset, e.g. the dev server
// reached over a bare LAN hostname - the guard compares the Origin header's
// host against the request's own Host instead, so a same-origin call still
// passes.
//
// The decision order on an unsafe request is:
//
//  1. Sec-Fetch-Site present: allow "same-origin" and "same-site"; reject
//     "cross-site" and "none" (a "none" navigation should never reach a
//     state-changing fetch). Modern browsers always send this header on
//     fetch/XHR, so it is the primary signal.
//  2. Otherwise, if Origin is present: allow only when its scheme+host matches
//     the expected origin; reject any mismatch.
//  3. If neither header is present: allow. Browsers send Origin on every
//     state-changing cross-origin AND same-origin fetch, so a missing Origin
//     means a non-browser API client (curl, a native app, server-to-server) -
//     which CSRF does not apply to - or a same-origin top-level navigation that
//     cannot carry an attacker's cookies cross-site under SameSite=Lax anyway.
func sameOriginCheck(expectedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)

			return
		}

		if allowedBySameOrigin(r, expectedOrigin) {
			next.ServeHTTP(w, r)

			return
		}

		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
	})
}

// isSafeMethod reports whether method is "safe" per RFC 9110 (no state change).
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// allowedBySameOrigin applies the decision order documented on sameOriginCheck.
func allowedBySameOrigin(r *http.Request, expectedOrigin string) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "same-site"
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	return originMatches(origin, expectedOrigin, r.Host)
}

// originMatches reports whether the Origin header's scheme+host matches the
// server's own origin. It compares the Origin against expectedOrigin (the
// normalized scheme://host from cfg.BaseURL) when that is set; otherwise it
// falls back to the request's Host, treating the Origin as same-origin when
// their host parts agree.
func originMatches(origin, expectedOrigin, requestHost string) bool {
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		return false
	}

	if expectedOrigin != "" {
		return normalizeOrigin(originURL.Scheme, originURL.Host) == expectedOrigin
	}

	return strings.EqualFold(originURL.Host, requestHost)
}

// originFromBaseURL extracts the normalized scheme://host origin from a
// configured base URL, dropping any path, query, or trailing slash. It returns
// "" when baseURL is empty or unparseable, which signals sameOriginCheck to
// fall back to the request Host.
func originFromBaseURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}

	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}

	return normalizeOrigin(u.Scheme, u.Host)
}

// normalizeOrigin returns a canonical lower-cased scheme://host so a
// case-insensitive Origin comparison reduces to plain string equality.
func normalizeOrigin(scheme, host string) string {
	return strings.ToLower(scheme) + "://" + strings.ToLower(host)
}
