package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

// One-shot signed cookie carrying the verify-email/pending banner
// across the 303 from POST /verify-email/resend. PRG keeps refresh
// from re-issuing the request; HMAC signing keeps an injected banner
// out of the template.
const verifyFlashCookieName = "topbanana_verify_flash"

// verifyFlashKind distinguishes notice vs error banners. The byte
// values are part of the cookie wire format - changing them retires
// any in-flight cookie.
type verifyFlashKind byte

const (
	verifyFlashNotice verifyFlashKind = 'n'
	verifyFlashError  verifyFlashKind = 'e'
)

// verifyFlashMaxAge bounds the gap between Set and the follow-up GET.
// 15 seconds covers a slow 303 hop without leaving a stale banner if
// the user closes the tab mid-redirect.
const verifyFlashMaxAge = 15

// verifyFlashWaitSep separates the optional wait-seconds hint from the
// message in the cookie payload. ASCII unit separator so a verbatim
// SMTP error string in the message cannot collide.
const verifyFlashWaitSep = "\x1f"

// VerifyFlash signs and verifies the one-shot verify-pending banner.
type VerifyFlash struct {
	key           []byte
	secureCookies bool
}

// NewVerifyFlash returns a flash helper. secureCookies follows
// [session.Manager]: production true, dev false (#205).
func NewVerifyFlash(key []byte, secureCookies bool) *VerifyFlash {
	return &VerifyFlash{key: key, secureCookies: secureCookies}
}

// SetNotice stashes a success banner for the next GET /verify-email/pending.
func (f *VerifyFlash) SetNotice(w http.ResponseWriter, msg string) {
	f.set(w, verifyFlashNotice, msg, 0)
}

// SetError stashes an error banner. wait is the rate-limit hint in
// seconds (0 for non-rate-limited errors); when non-zero the template
// renders the resend button as disabled with the countdown.
func (f *VerifyFlash) SetError(w http.ResponseWriter, msg string, wait int) {
	f.set(w, verifyFlashError, msg, wait)
}

// VerifyFlashRead is the result of Read. OK=false leaves the other
// fields zero. Notice and Err are populated mutually exclusively
// according to which Set helper wrote the cookie.
type VerifyFlashRead struct {
	Notice      string
	Err         string
	WaitSeconds int
	OK          bool
}

// Read returns the stashed banner and clears the cookie. OK=false for
// missing, malformed, or bad-signature cookies.
func (f *VerifyFlash) Read(w http.ResponseWriter, r *http.Request) VerifyFlashRead {
	c, err := r.Cookie(verifyFlashCookieName)
	if err != nil {
		return VerifyFlashRead{}
	}
	// Clear unconditionally so a malformed cookie cannot persist.
	http.SetCookie(w, f.cookie("", -1))

	payloadPart, macPart, sep := strings.Cut(c.Value, ".")
	if !sep {
		return VerifyFlashRead{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return VerifyFlashRead{}
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return VerifyFlashRead{}
	}
	if !hmac.Equal(gotMAC, f.sign(payload)) {
		return VerifyFlashRead{}
	}

	return decodeVerifyFlash(payload)
}

func (f *VerifyFlash) set(w http.ResponseWriter, kind verifyFlashKind, msg string, wait int) {
	payload := encodeVerifyFlash(kind, msg, wait)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(f.sign(payload))
	http.SetCookie(w, f.cookie(value, verifyFlashMaxAge))
}

func (f *VerifyFlash) cookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure follows cfg.SecureCookies() like the session cookie (#205).
	return &http.Cookie{
		Name:     verifyFlashCookieName,
		Value:    value,
		Path:     "/verify-email",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   f.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

func (f *VerifyFlash) sign(payload []byte) []byte {
	h := hmac.New(sha256.New, f.key)
	_, _ = h.Write(payload)

	return h.Sum(nil)
}

func encodeVerifyFlash(kind verifyFlashKind, msg string, wait int) []byte {
	var sb strings.Builder
	sb.WriteByte(byte(kind))
	if kind == verifyFlashError && wait > 0 {
		sb.WriteString(strconv.Itoa(wait))
	}
	sb.WriteString(verifyFlashWaitSep)
	sb.WriteString(msg)

	return []byte(sb.String())
}

func decodeVerifyFlash(payload []byte) VerifyFlashRead {
	if len(payload) < 1 {
		return VerifyFlashRead{}
	}
	kind := verifyFlashKind(payload[0])
	if kind != verifyFlashNotice && kind != verifyFlashError {
		return VerifyFlashRead{}
	}
	rest := string(payload[1:])
	waitPart, msg, sep := strings.Cut(rest, verifyFlashWaitSep)
	if !sep {
		return VerifyFlashRead{}
	}
	wait := 0
	if waitPart != "" {
		parsed, err := strconv.Atoi(waitPart)
		if err != nil || parsed < 0 {
			return VerifyFlashRead{}
		}
		wait = parsed
	}

	out := VerifyFlashRead{OK: true, WaitSeconds: wait}
	if kind == verifyFlashNotice {
		out.Notice = msg
	} else {
		out.Err = msg
	}

	return out
}
