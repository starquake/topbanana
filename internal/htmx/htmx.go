// Package htmx owns the htmx wire conventions (header names + values) so a
// typo in one handler can't silently downgrade a partial swap to a full-page
// redirect.
package htmx

import "net/http"

// Wire `HX-Request` canonicalises to `Hx-Request` via [http.Header].
const requestHeader = "Hx-Request"

// IsRequest reports whether the request was fired by htmx.
func IsRequest(r *http.Request) bool {
	return r.Header.Get(requestHeader) == "true"
}
