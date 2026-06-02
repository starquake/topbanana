// Package csrf provides CSRF protection for unsafe HTTP methods.
//
// The defence is the standard signed-double-submit pattern using stdlib
// primitives only:
//
//  1. The first GET request to a form-rendering page receives a per-session
//     nonce in the "tb_csrf_nonce" cookie.
//  2. The same response embeds a hidden form field ("csrf_token") whose value
//     is HMAC-SHA256(csrfKey, nonce), base64url-encoded.
//  3. On a subsequent unsafe request the middleware reads the nonce cookie,
//     recomputes the expected HMAC, and compares it to the submitted token in
//     constant time. A mismatch results in 403.
//
// The CSRF signing key is derived from the application's SESSION_KEY via
// HMAC-SHA256(sessionKey, "csrf-v1") so deployments do not need to manage a
// second secret, and the two keys cannot be confused with each other.
//
// Cookies use Path=/, SameSite=Lax, and HttpOnly=true. The Secure attribute
// is controlled per Manager via [New]'s secureCookies argument: production
// callers pass true; development callers pass false so the dev server is
// reachable over plain HTTP from any LAN hostname (see #205). The cookie
// rides along automatically on same-origin form submits regardless of
// HttpOnly, so making it readable from JavaScript would only add risk
// without enabling any current flow.
package csrf

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
)

// CookieName is the name of the CSRF nonce cookie.
const CookieName = "tb_csrf_nonce"

// FormField is the name of the hidden form field carrying the CSRF token.
const FormField = "csrf_token"

// MaxAge is the lifetime of the CSRF nonce cookie in seconds (30 days).
// It must be at least the session cookie lifetime (internal/session.MaxAge):
// a nonce backs the csrf_token rendered into every form, so if it expires
// while the session is still valid, a still-signed-in user submitting a
// form rendered earlier (e.g. a logout button on a tab open past the old
// 24h) gets a spurious 403 (#614). Invariant pinned by
// TestCSRFCookieOutlivesSession.
const MaxAge = 30 * 24 * 60 * 60

// nonceByteLength is the length of the random nonce written to the cookie.
const nonceByteLength = 16

// keyDerivationLabel is mixed into the SESSION_KEY to derive the CSRF signing
// key. Versioned so we can rotate the derivation later without breaking
// existing deployments.
const keyDerivationLabel = "csrf-v1"

// ErrInvalidToken is returned by Validate when the cookie or form token is
// missing, malformed, or does not match. Callers (typically the middleware)
// map it to HTTP 403.
var ErrInvalidToken = errors.New("csrf: invalid or missing token")

// Manager issues CSRF nonce cookies and validates tokens submitted with unsafe
// requests. A single instance is safe for concurrent use.
type Manager struct {
	key           []byte
	secureCookies bool
}

// New returns a Manager whose signing key is derived from the given session
// key using HMAC-SHA256. Passing the same SESSION_KEY produces the same CSRF
// key so the manager survives process restarts without invalidating tokens.
// secureCookies controls the Secure attribute on issued nonce cookies - see
// Config.SecureCookies in internal/config for the policy and #205 for why
// it is gated.
func New(sessionKey []byte, secureCookies bool) *Manager {
	h := hmac.New(sha256.New, sessionKey)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(keyDerivationLabel))

	return &Manager{key: h.Sum(nil), secureCookies: secureCookies}
}

// Token returns a CSRF token for the current request, ensuring a nonce cookie
// is set on the response when one is not already present. Call at most once
// per response: a second call without a pre-existing cookie would issue a new
// nonce, overwrite the previous Set-Cookie, and produce a token that no
// longer matches the form rendered from the first call.
//
// Callers that already wrote a response status should still call Token before
// flushing the body - [http.SetCookie] writes a header, which only takes effect
// before WriteHeader.
func (m *Manager) Token(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return tokenFromNonce(m.key, c.Value)
	}

	nonce := newNonce()
	http.SetCookie(w, m.newCookie(nonce))

	return tokenFromNonce(m.key, nonce)
}

// Validate reads the nonce cookie and the form's csrf_token field, recomputes
// the expected HMAC, and compares it to the submitted token in constant time.
// It returns ErrInvalidToken on any mismatch, missing data, or parse error.
//
// Validate calls r.ParseForm if the form has not yet been parsed. Callers can
// safely call r.PostFormValue or r.ParseForm again afterward.
func (m *Manager) Validate(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ErrInvalidToken
	}

	if parseErr := r.ParseForm(); parseErr != nil {
		return ErrInvalidToken
	}

	submitted := r.PostFormValue(FormField)
	if submitted == "" {
		return ErrInvalidToken
	}

	expected := tokenFromNonce(m.key, c.Value)

	gotBytes, err := base64.RawURLEncoding.DecodeString(submitted)
	if err != nil {
		return ErrInvalidToken
	}
	wantBytes, err := base64.RawURLEncoding.DecodeString(expected)
	if err != nil {
		return ErrInvalidToken
	}

	if subtle.ConstantTimeCompare(gotBytes, wantBytes) != 1 {
		return ErrInvalidToken
	}

	return nil
}

// Middleware enforces CSRF protection on unsafe HTTP methods. Safe methods
// (GET, HEAD, OPTIONS) pass through unchanged so renderers can still set the
// nonce cookie on the response. POST, PUT, PATCH, and DELETE must carry a
// matching token; otherwise the request is short-circuited with 403.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)

			return
		}
		if err := m.Validate(r); err != nil {
			http.Error(w, "forbidden: invalid CSRF token", http.StatusForbidden)

			return
		}
		next.ServeHTTP(w, r)
	})
}

// isSafeMethod reports whether the given HTTP method is "safe" per RFC 9110
// (no state change). Safe methods bypass CSRF validation.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// tokenFromNonce returns the base64url-encoded HMAC-SHA256 of the nonce.
func tokenFromNonce(key []byte, nonce string) string {
	h := hmac.New(sha256.New, key)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(nonce))

	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// newNonce returns a fresh random nonce, base64url-encoded.
func newNonce() string {
	b := make([]byte, nonceByteLength)
	// crypto/rand.Read in Go 1.24+ never returns an error.
	_, _ = rand.Read(b)

	return base64.RawURLEncoding.EncodeToString(b)
}

// newCookie returns a fresh CSRF nonce cookie. HttpOnly and SameSite=Lax
// are always set; the Secure attribute follows the Manager's
// secureCookies field - see [New]'s doc comment for the rationale.
func (m *Manager) newCookie(value string) *http.Cookie {
	//nolint:gosec // G124: Secure is intentionally policy-driven (production
	// passes true via cfg.SecureCookies(); dev passes false so plain-HTTP
	// LAN access works). See #205.
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   MaxAge,
		HttpOnly: true,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}
