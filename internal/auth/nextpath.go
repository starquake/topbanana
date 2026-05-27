package auth

import (
	"net/url"
	"strings"
)

// SafeNextPath returns raw if it is a same-site relative path safe to
// hand to [http.Redirect] after login, otherwise the empty string.
// Used by the middlewares to thread the original destination through
// the login flow without enabling an open redirect.
//
// The accepted shape is a path that:
//   - starts with exactly one `/` (no `//` which is a protocol-relative URL),
//   - contains no `\` (Windows-style path traversal that some browsers
//     normalise), in literal or percent-encoded form,
//   - parses cleanly via [url.Parse] with no Host, Scheme, Opaque, or User segment,
//   - does not point at an auth page (`/login`, `/register`) so the
//     post-login redirect can never loop back into the login flow.
//
// Any other input - empty string, absolute URL, `javascript:` scheme,
// `//evil.com`, embedded backslash, `/login?next=/login` - returns "".
// The caller falls back to the role-appropriate landing in that case.
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
