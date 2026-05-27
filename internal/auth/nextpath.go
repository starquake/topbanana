package auth

import (
	"net/url"
	"strings"
)

// SafeNextPath returns raw if it is a same-site relative path safe to
// pass to [http.Redirect] after login, otherwise "". Rejects
// protocol-relative URLs, backslash variants, anything with a scheme or
// host, and the auth pages themselves (so `/login?next=/login` cannot
// loop).
func SafeNextPath(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.Contains(raw, "\\") || strings.Contains(strings.ToLower(raw), "%5c") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return ""
	}
	// Reject paths that re-enter the auth flow. Without this guard a
	// signed-in user landing on `/login?next=/login` 303s in a loop.
	if u.Path == "/login" || u.Path == "/register" {
		return ""
	}

	return raw
}
