// Package htmx centralises the small set of htmx-specific conventions the
// backend touches. htmx is wired up client-side via the vendored
// internal/assets/static/js/htmx.min.js and triggered from `hx-post` / `hx-get`
// attributes in admin templates; handlers gate partial-vs-redirect responses
// on the HX-Request header it sets.
//
// Keep this package minimal: it owns the wire conventions (header names and
// values), nothing more. Handlers that need to render a partial keep that
// partial in their own package; they only ask htmx whether the caller wants
// one.
package htmx

import "net/http"

// requestHeader is the request marker htmx sets on every XHR it fires. Go's
// [http.Header] canonicalisation maps the wire `HX-Request` to `Hx-Request`,
// so callers must read it via Header.Get with the canonical form. We pin the
// name here so a typo at any one handler site can't silently downgrade the
// response to a full-page redirect.
const requestHeader = "Hx-Request"

// IsRequest reports whether the request was fired by htmx (rather than a plain
// form submit or a programmatic POST). Handlers use it to swap between
// partial-content responses (an empty 200 for outerHTML swap, or a small HTML
// fragment) and the no-JS redirect-after-POST response.
func IsRequest(r *http.Request) bool {
	return r.Header.Get(requestHeader) == "true"
}
