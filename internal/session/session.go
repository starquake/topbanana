// Package session provides signed-cookie session helpers for storing
// the authenticated player ID plus the session-version stamp.
//
// Cookies issued today are three pipe-separated fields signed as one
// payload:
//
//	base64url(playerID|sessionVersion|issuedAt) + "." +
//	  base64url(hmac_sha256(key, playerID|sessionVersion|issuedAt))
//
// Two-field cookies minted before #112 PR3 (playerID|issuedAt) are
// still accepted with an implicit sessionVersion=0 so a deploy does
// not invalidate every live session. The decoder returns the parsed
// version; the auth middleware compares it against the player's
// current players.session_version and treats a mismatch as an
// unauthenticated request - this is how a password reset invalidates
// every cookie minted before the reset.
//
// MaxAge is both the Max-Age cookie attribute and the server-side
// accept window; a cookie older than MaxAge is rejected even with a
// valid signature so a copied cookie cannot be replayed indefinitely.
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
	key           []byte
	now           func() time.Time
	secureCookies bool
}

// New returns a Manager that signs cookies with the given key. secureCookies
// controls the Secure attribute on issued cookies - see Config.SecureCookies
// in internal/config for the dev-versus-production policy and #205 for why
// this is gated.
func New(key []byte, secureCookies bool) *Manager {
	return newWithClock(key, secureCookies, time.Now)
}

// newWithClock returns a Manager with a caller-supplied clock. It exists for
// internal tests that need to fix time without depending on a package-level
// variable.
func newWithClock(key []byte, secureCookies bool, now func() time.Time) *Manager {
	return &Manager{key: key, now: now, secureCookies: secureCookies}
}

// Set writes a signed session cookie carrying the player ID and the
// session_version stamp the cookie was issued at. The caller passes
// the value from players.session_version on the row being signed in;
// a later password reset bumps that column and so invalidates this
// cookie at the auth-middleware check.
func (m *Manager) Set(w http.ResponseWriter, playerID, sessionVersion int64) {
	http.SetCookie(w, m.newCookie(encode(playerID, sessionVersion, m.now().Unix(), m.key), MaxAge))
}

// Clear deletes the session cookie by setting it with an empty value and a negative MaxAge.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, m.newCookie("", clearedMaxAge))
}

// PlayerID returns the player ID encoded in the request's session cookie.
// It returns ok=false when the cookie is missing, malformed, has a bad
// signature, or was issued more than MaxAge seconds ago. Callers that
// also need the session-version stamp (the auth middleware's
// password-reset invalidation check) should use [Manager.Decode]
// instead.
func (m *Manager) PlayerID(r *http.Request) (int64, bool) {
	id, _, ok := m.Decode(r)

	return id, ok
}

// Decode returns the player ID and session-version stamp encoded in
// the request's session cookie. Two-field legacy cookies (pre-#112
// PR3) decode with sessionVersion=0. Returns ok=false on the same
// conditions as [Manager.PlayerID]: missing cookie, malformed
// payload, bad signature, or age greater than MaxAge.
func (m *Manager) Decode(r *http.Request) (playerID, sessionVersion int64, ok bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return 0, 0, false
	}

	return decode(c.Value, m.key, m.now)
}

// newCookie returns the session cookie with the safe defaults always applied:
// HttpOnly and SameSite=Lax. The Secure attribute follows the Manager's
// secureCookies field - see [New]'s doc comment for the rationale.
//
// SameSite=Lax is load-bearing CSRF defence for the /api/* routes, which
// authenticate via this cookie. A same-origin guard on unsafe methods backs
// it up (see addAPIRoutes in internal/server/routes.go), but that guard allows
// header-less non-browser clients through, so do not weaken SameSite to None
// on the strength of it.
func (m *Manager) newCookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure is intentionally policy-driven (production
	// passes true via cfg.SecureCookies(); dev passes false so plain-HTTP
	// LAN access works). See #205.
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

func encode(playerID, sessionVersion, issuedAt int64, key []byte) string {
	payload := strconv.FormatInt(playerID, integerBase) + "|" +
		strconv.FormatInt(sessionVersion, integerBase) + "|" +
		strconv.FormatInt(issuedAt, integerBase)
	payloadPart := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := sign([]byte(payload), key)
	macPart := base64.RawURLEncoding.EncodeToString(mac)

	return payloadPart + "." + macPart
}

func decode(value string, key []byte, now func() time.Time) (playerID, sessionVersion int64, ok bool) {
	payloadPart, macPart, sep := strings.Cut(value, ".")
	if !sep {
		return 0, 0, false
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return 0, 0, false
	}

	gotMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return 0, 0, false
	}

	wantMAC := sign(payloadBytes, key)
	if !hmac.Equal(gotMAC, wantMAC) {
		return 0, 0, false
	}

	p, ok := parsePayload(string(payloadBytes))
	if !ok {
		return 0, 0, false
	}

	// "Expired" means strictly older than MaxAge: age == MaxAge is still valid.
	if now().Unix()-p.IssuedAt > MaxAge {
		return 0, 0, false
	}

	return p.PlayerID, p.SessionVersion, true
}

// decodedPayload is the parsed form of the cookie's pipe-separated
// payload. Stays an unexported struct so the multi-value return on
// parsePayload does not blow past revive's function-result-limit cap.
type decodedPayload struct {
	PlayerID       int64
	SessionVersion int64
	IssuedAt       int64
}

// parsePayload accepts both the new three-field (playerID|sessionVersion|issuedAt)
// and the legacy two-field (playerID|issuedAt) wire formats. Legacy
// cookies decode with SessionVersion=0 so a deploy does not log
// everyone out; the version-mismatch check at the middleware then
// only fires for accounts whose session_version has actually moved.
func parsePayload(payload string) (decodedPayload, bool) {
	parts := strings.Split(payload, "|")
	switch len(parts) {
	case legacyPayloadParts:
		id, err := strconv.ParseInt(parts[0], integerBase, playerIDBitSize)
		if err != nil {
			return decodedPayload{}, false
		}
		ts, err := strconv.ParseInt(parts[1], integerBase, issuedAtBitSize)
		if err != nil {
			return decodedPayload{}, false
		}

		return decodedPayload{PlayerID: id, IssuedAt: ts}, true
	case versionedPayloadParts:
		id, err := strconv.ParseInt(parts[0], integerBase, playerIDBitSize)
		if err != nil {
			return decodedPayload{}, false
		}
		v, err := strconv.ParseInt(parts[1], integerBase, sessionVersionBitSize)
		if err != nil {
			return decodedPayload{}, false
		}
		ts, err := strconv.ParseInt(parts[2], integerBase, issuedAtBitSize)
		if err != nil {
			return decodedPayload{}, false
		}

		return decodedPayload{PlayerID: id, SessionVersion: v, IssuedAt: ts}, true
	default:
		return decodedPayload{}, false
	}
}

const (
	legacyPayloadParts    = 2
	versionedPayloadParts = 3
	sessionVersionBitSize = 64
)

func sign(payload, key []byte) []byte {
	h := hmac.New(sha256.New, key)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write(payload)

	return h.Sum(nil)
}
