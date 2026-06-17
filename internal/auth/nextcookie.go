package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// googleNextCookieName is the sibling cookie that carries the
// validated post-login `next` path across the round trip to Google
// (#449). Set by HandleGoogleLogin when the request arrived with a
// safe ?next, cleared by HandleGoogleCallback. Value shape mirrors
// the state cookie: <base64url-encoded-path> "." <HMAC>.
const googleNextCookieName = "tb_google_next"

// googleNextCookie returns the sibling cookie that carries the
// validated next path across the OAuth round trip. Same Path /
// HttpOnly / Secure / SameSite policy as the state cookie so the two
// have identical browser semantics.
func googleNextCookie(value string, secure bool, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure follows cfg.SecureCookies() like the state cookie.
	return &http.Cookie{
		Name:     googleNextCookieName,
		Value:    value,
		Path:     "/login/google",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// signNext returns the wire shape `<base64url(path)>.<HMAC>`. HMAC is
// computed over the encoded path so a tampered cookie cannot resolve
// to a different SafeNextPath-valid value at read time.
func signNext(key []byte, path string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(path))
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(encoded))

	return encoded + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// readGoogleNext extracts and validates the next path from the
// sibling cookie. Returns "" on any failure path (missing cookie,
// malformed value, bad MAC, or a path SafeNextPath rejects). The
// signature check binds the cookie to this deployment's key; the
// SafeNextPath re-check defends against a cookie that was signed
// before the validator was tightened.
func readGoogleNext(r *http.Request, key []byte) string {
	cookie, err := r.Cookie(googleNextCookieName)
	if err != nil {
		return ""
	}
	encoded, mac, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return ""
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		return ""
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(encoded))
	if !hmac.Equal(gotMAC, h.Sum(nil)) {
		return ""
	}
	pathBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}

	return SafeNextPath(string(pathBytes))
}
