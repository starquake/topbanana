package locale

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// rootPath is the fallback redirect target when there is no usable next param.
const rootPath = "/"

// nextParam names the query parameter carrying the page to return to.
const nextParam = "next"

// HandleSetLocale sets the lang cookie from the {locale} path segment and
// redirects to the same-site path in the next query param. It is a plain GET
// link (no CSRF) since it only writes a preference cookie; an invalid locale is
// ignored.
func HandleSetLocale() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loc := r.PathValue("locale"); IsValid(loc) {
			SetCookie(w, loc)
		}

		//nolint:gosec // G710: next is sanitized to a same-site path by redirectTarget.
		http.Redirect(w, r, redirectTarget(r.URL.Query().Get(nextParam)), http.StatusSeeOther)
	})
}

// SwitchHref returns the switcher link for loc that returns to the current
// page. Only a GET page is returned to: a POST-rendered page may have no GET
// route.
func SwitchHref(r *http.Request, loc string) string {
	href := "/lang/" + url.PathEscape(loc)
	if r.Method != http.MethodGet {
		return href
	}
	// A one-time credential (reset, verify and invite tokens, the OAuth code)
	// must not be copied into another URL.
	q := r.URL.Query()
	if slices.ContainsFunc([]string{"token", "code"}, q.Has) {
		return href
	}

	return href + "?" + nextParam + "=" + url.QueryEscape(r.URL.RequestURI())
}

// redirectTarget returns next's path + query, never a scheme or host, so it
// cannot become an open redirect. Falls back to "/" for anything else.
func redirectTarget(next string) string {
	// Browsers read a backslash as a slash, so "/\host" would leave the site.
	if next == "" || strings.Contains(next, "\\") || strings.Contains(strings.ToLower(next), "%5c") {
		return rootPath
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return rootPath
	}
	dest := u.EscapedPath()
	// A "//host/..." path would be emitted as a protocol-relative Location.
	if !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		return rootPath
	}
	if u.RawQuery != "" {
		dest += "?" + u.RawQuery
	}

	return dest
}
