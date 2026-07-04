package locale

import (
	"net/http"
	"net/url"
	"strings"
)

// rootPath is the fallback redirect target when there is no usable Referer.
const rootPath = "/"

// HandleSetLocale sets the lang cookie from the {locale} path segment and
// redirects back to where the request came from. It is a plain GET link (no
// CSRF) because it only writes a preference cookie; an invalid locale is
// ignored so a stray value never clears a good choice.
func HandleSetLocale() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loc := r.PathValue("locale"); IsValid(loc) {
			SetCookie(w, loc)
		}

		// redirectTarget returns only the Referer's path + query (never an
		// absolute off-site URL), so this cannot be an open redirect.
		//nolint:gosec // G710: destination is a same-site path derived in redirectTarget, not raw request input.
		http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
	})
}

// redirectTarget returns a safe same-site path to return to after switching:
// only the Referer's path + query, never its host, so it cannot become an open
// redirect. Falls back to "/" when there is no usable Referer.
func redirectTarget(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return rootPath
	}
	u, err := url.Parse(ref)
	if err != nil {
		return rootPath
	}
	dest := u.EscapedPath()
	// Require a single-slash-rooted path. A "//host/..." path would be emitted
	// as a protocol-relative Location and redirect off-site, so reject it (and
	// any non-rooted value) back to the home page.
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		return rootPath
	}
	if u.RawQuery != "" {
		dest += "?" + u.RawQuery
	}

	return dest
}
