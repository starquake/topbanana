package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

// signedFlashKind tags a flash entry as a notice or an error. The byte
// values are part of the cookie wire format - changing them retires
// any in-flight cookie signed by the previous version.
type signedFlashKind byte

const (
	signedFlashNotice signedFlashKind = 'n'
	signedFlashError  signedFlashKind = 'e'
)

// signedFlashMaxAge bounds the gap between Set and the follow-up GET.
// 15 seconds covers a slow 303 hop without leaving a stale banner if
// the user closes the tab mid-redirect.
const signedFlashMaxAge = 15

// signedFlashWaitSep is the ASCII unit separator so a verbatim error
// string in the message cannot collide with the wait-seconds prefix.
const signedFlashWaitSep = "\x1f"

// SignedFlash signs and verifies a one-shot banner cookie. One
// instance per (cookieName, cookiePath) so the verify, forgot, and
// any future reset-style flow each carry their own state. HMAC-signed
// so an attacker-injected cookie cannot reach the template; cleared
// on Read so refresh is safe.
type SignedFlash struct {
	key           []byte
	secureCookies bool
	cookieName    string
	cookiePath    string
}

// NewSignedFlash returns a flash helper bound to the given cookie name
// and path. secureCookies follows [session.Manager]: production true,
// dev false (#205). Path scopes which routes receive the cookie - the
// verify flow uses /verify-email so /forgot-password cannot read its
// banner, and vice versa.
func NewSignedFlash(key []byte, secureCookies bool, cookieName, cookiePath string) *SignedFlash {
	return &SignedFlash{
		key:           key,
		secureCookies: secureCookies,
		cookieName:    cookieName,
		cookiePath:    cookiePath,
	}
}

// SetNotice stashes a success banner for the next GET on the cookie's path.
func (f *SignedFlash) SetNotice(w http.ResponseWriter, msg string) {
	f.set(w, signedFlashNotice, msg, 0)
}

// SetError stashes an error banner. wait is the rate-limit hint in
// seconds (0 for non-rate-limited errors); when non-zero the template
// renders the submit button disabled with the countdown.
func (f *SignedFlash) SetError(w http.ResponseWriter, msg string, wait int) {
	f.set(w, signedFlashError, msg, wait)
}

// SignedFlashRead is the result of Read. OK=false leaves the other
// fields zero. Notice and Err are populated mutually exclusively
// according to which Set helper wrote the cookie.
type SignedFlashRead struct {
	Notice      string
	Err         string
	WaitSeconds int
	OK          bool
}

// Read returns the stashed banner and clears the cookie. OK=false for
// missing, malformed, or bad-signature cookies.
func (f *SignedFlash) Read(w http.ResponseWriter, r *http.Request) SignedFlashRead {
	c, err := r.Cookie(f.cookieName)
	if err != nil {
		return SignedFlashRead{}
	}
	// Clear unconditionally so a malformed cookie cannot persist.
	http.SetCookie(w, f.cookie("", -1))

	payloadPart, macPart, sep := strings.Cut(c.Value, ".")
	if !sep {
		return SignedFlashRead{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return SignedFlashRead{}
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return SignedFlashRead{}
	}
	if !hmac.Equal(gotMAC, f.sign(payload)) {
		return SignedFlashRead{}
	}

	return decodeSignedFlash(payload)
}

func (f *SignedFlash) set(w http.ResponseWriter, kind signedFlashKind, msg string, wait int) {
	payload := encodeSignedFlash(kind, msg, wait)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(f.sign(payload))
	http.SetCookie(w, f.cookie(value, signedFlashMaxAge))
}

func (f *SignedFlash) cookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure follows cfg.SecureCookies() like the session cookie (#205).
	return &http.Cookie{
		Name:     f.cookieName,
		Value:    value,
		Path:     f.cookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   f.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

func (f *SignedFlash) sign(payload []byte) []byte {
	h := hmac.New(sha256.New, f.key)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write(payload)

	return h.Sum(nil)
}

func encodeSignedFlash(kind signedFlashKind, msg string, wait int) []byte {
	var sb strings.Builder
	sb.WriteByte(byte(kind))
	if kind == signedFlashError && wait > 0 {
		sb.WriteString(strconv.Itoa(wait))
	}
	sb.WriteString(signedFlashWaitSep)
	sb.WriteString(msg)

	return []byte(sb.String())
}

func decodeSignedFlash(payload []byte) SignedFlashRead {
	if len(payload) < 1 {
		return SignedFlashRead{}
	}
	kind := signedFlashKind(payload[0])
	if kind != signedFlashNotice && kind != signedFlashError {
		return SignedFlashRead{}
	}
	rest := string(payload[1:])
	waitPart, msg, sep := strings.Cut(rest, signedFlashWaitSep)
	if !sep {
		return SignedFlashRead{}
	}
	wait := 0
	if waitPart != "" {
		parsed, err := strconv.Atoi(waitPart)
		if err != nil || parsed < 0 {
			return SignedFlashRead{}
		}
		wait = parsed
	}

	out := SignedFlashRead{OK: true, WaitSeconds: wait}
	if kind == signedFlashNotice {
		out.Notice = msg
	} else {
		out.Err = msg
	}

	return out
}

// VerifyFlashCookieName / VerifyFlashCookiePath / ForgotFlashCookieName /
// ForgotFlashCookiePath are the per-flow constants the wiring layer
// uses to build a [SignedFlash]. Exported so a router test can match
// the cookie set on a response without re-declaring the string.
const (
	VerifyFlashCookieName = "topbanana_verify_flash"
	VerifyFlashCookiePath = "/verify-email"
	ForgotFlashCookieName = "topbanana_forgot_flash"
	ForgotFlashCookiePath = "/forgot-password"
)
