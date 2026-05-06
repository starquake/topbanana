// Package session provides signed-cookie session helpers for storing the authenticated player ID.
//
// The cookie value is the player ID and the issued-at unix timestamp joined by "|", followed by
// an HMAC-SHA256 signature, all base64url encoded:
//
//	base64url(playerID|issuedAt) + "." + base64url(hmac_sha256(key, playerID|issuedAt))
//
// The cookie is HttpOnly, SameSite=Lax, and Secure. Browsers treat http://localhost as a
// secure context, so Secure cookies still work in local development without TLS.
//
// MaxAge serves double duty: it is both the client-side cookie lifetime and the
// server-side accept window. A cookie whose issuedAt is older than MaxAge seconds
// is rejected even if the signature is still valid, so a copied cookie cannot be
// replayed indefinitely.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CookieName is the name of the session cookie.
const CookieName = "topbanana_session"

// MaxAge is the lifetime of a session cookie in seconds (30 days). It is also the
// server-side window: a cookie issued more than MaxAge seconds ago is rejected.
const MaxAge = 30 * 24 * 60 * 60

// clearedMaxAge is the MaxAge value used to clear a cookie.
const clearedMaxAge = -1

// integerBase is the base used to encode and parse the player ID inside the cookie.
const integerBase = 10

// playerIDBitSize is the bit size used when parsing the player ID.
const playerIDBitSize = 64

// issuedAtBitSize is the bit size used when parsing the issuedAt timestamp.
const issuedAtBitSize = 64

// Manager signs and verifies session cookies. It bundles the signing key and
// the clock so callers do not have to thread these parameters through every
// call site, and so tests can fix the clock without touching package-level state.
type Manager struct {
	key []byte
	now func() time.Time
}

// New returns a Manager that signs cookies with the given key.
func New(key []byte) *Manager {
	return newWithClock(key, time.Now)
}

// newWithClock returns a Manager with a caller-supplied clock. It exists for
// internal tests that need to fix time without depending on a package-level
// variable.
func newWithClock(key []byte, now func() time.Time) *Manager {
	return &Manager{key: key, now: now}
}

// Set writes a signed session cookie containing the given player ID to w.
func (m *Manager) Set(w http.ResponseWriter, playerID int64) {
	http.SetCookie(w, newCookie(encode(playerID, m.now().Unix(), m.key), MaxAge))
}

// Clear deletes the session cookie by setting it with an empty value and a negative MaxAge.
func (*Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, newCookie("", clearedMaxAge))
}

// PlayerID returns the player ID encoded in the request's session cookie.
// It returns ok=false when the cookie is missing, malformed, has a bad signature,
// or was issued more than MaxAge seconds ago.
func (m *Manager) PlayerID(r *http.Request) (int64, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return 0, false
	}

	return decode(c.Value, m.key, m.now)
}

// newCookie returns the session cookie with the safe defaults always applied:
// HttpOnly, SameSite=Lax, and Secure. Browsers treat http://localhost as a
// secure context, so the Secure flag does not break local development.
func newCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

func encode(playerID, issuedAt int64, key []byte) string {
	payload := strconv.FormatInt(playerID, integerBase) + "|" + strconv.FormatInt(issuedAt, integerBase)
	payloadPart := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := sign([]byte(payload), key)
	macPart := base64.RawURLEncoding.EncodeToString(mac)

	return payloadPart + "." + macPart
}

func decode(value string, key []byte, now func() time.Time) (int64, bool) {
	payloadPart, macPart, ok := strings.Cut(value, ".")
	if !ok {
		return 0, false
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return 0, false
	}

	gotMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return 0, false
	}

	wantMAC := sign(payloadBytes, key)
	if !hmac.Equal(gotMAC, wantMAC) {
		return 0, false
	}

	idStr, issuedAtStr, ok := strings.Cut(string(payloadBytes), "|")
	if !ok {
		return 0, false
	}

	playerID, err := strconv.ParseInt(idStr, integerBase, playerIDBitSize)
	if err != nil {
		return 0, false
	}

	issuedAt, err := strconv.ParseInt(issuedAtStr, integerBase, issuedAtBitSize)
	if err != nil {
		return 0, false
	}

	// "Expired" means strictly older than MaxAge: age == MaxAge is still valid.
	if now().Unix()-issuedAt > MaxAge {
		return 0, false
	}

	return playerID, true
}

func sign(payload, key []byte) []byte {
	h := hmac.New(sha256.New, key)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write(payload)

	return h.Sum(nil)
}
