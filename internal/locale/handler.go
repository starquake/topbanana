package locale

import (
	"net/http"
	"net/url"
	"strings"
)

// rootPath is the fallback redirect target when there is no usable Referer.
const rootPath = "/"

// HandleSetLocale sets the lang cookie from the {locale} path segment and
// redirects back to the referring page. It is a plain GET link (no CSRF) since
// it only writes a preference cookie; an invalid locale is ignored.
func HandleSetLocale() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loc := r.PathValue("locale"); IsValid(loc) {
			SetCookie(w, loc)
		}

		//nolint:gosec // G710: destination is a same-site path derived in redirectTarget, not raw request input.
		http.Redirect(w, r, redirectTarget(r), http.StatusSeeOther)
	})
}

// redirectTarget returns the Referer's path + query, never its host, so it
// cannot become an open redirect. Falls back to "/" when there is no Referer.
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
	// Reject a "//host/..." path: it would be emitted as a protocol-relative
	// Location and redirect off-site. Also reject any non-rooted or empty path.
	if !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		return rootPath
	}
	if u.RawQuery != "" {
		dest += "?" + u.RawQuery
	}

	return dest
}
