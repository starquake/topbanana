package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

// One-shot cookie carrying the POST /admin/email/test banner across
// the 303 to /admin/email. PRG keeps Firefox from prompting "resend
// this form?" on refresh (#321). HMAC-signed so an injected banner
// cannot reach the template.
const emailFlashCookieName = "topbanana_admin_email_flash"

// FlashKind tags the one-shot banner as a notice or error.
type FlashKind byte

// FlashNotice and FlashError are the supported banner kinds. The
// byte values are part of the cookie wire format.
const (
	FlashNotice FlashKind = 'n'
	FlashError  FlashKind = 'e'
)

// flashMaxAge bounds the window between Set and the follow-up GET.
// Browsers follow the 303 in milliseconds; 15s covers slow connections
// without leaving a stale banner if the user closes the tab mid-hop.
const flashMaxAge = 15

// flashWaitSep separates the optional wait-seconds hint from the
// message in the payload. ASCII unit separator so a verbatim SMTP
// error cannot collide.
const flashWaitSep = "\x1f"

// EmailFlash signs and verifies the one-shot banner cookie.
type EmailFlash struct {
	key           []byte
	secureCookies bool
}

// NewEmailFlash returns a flash helper. secureCookies follows
// session.Manager: production true, dev false (#205).
func NewEmailFlash(key []byte, secureCookies bool) *EmailFlash {
	return &EmailFlash{key: key, secureCookies: secureCookies}
}

// SetNotice stashes a success banner for the next GET /admin/email.
func (f *EmailFlash) SetNotice(w http.ResponseWriter, msg string) {
	f.set(w, FlashNotice, msg, 0)
}

// SetError stashes an error banner; wait is the rate-limit hint in
// seconds (0 for non-rate-limited errors).
func (f *EmailFlash) SetError(w http.ResponseWriter, msg string, wait int) {
	f.set(w, FlashError, msg, wait)
}

// FlashRead is the result of Read. OK=false leaves the other fields
// zero.
type FlashRead struct {
	Kind FlashKind
	Msg  string
	Wait int
	OK   bool
}

// Read returns the stashed banner and clears the cookie. OK=false
// for missing, malformed, or bad-signature cookies.
func (f *EmailFlash) Read(w http.ResponseWriter, r *http.Request) FlashRead {
	c, err := r.Cookie(emailFlashCookieName)
	if err != nil {
		return FlashRead{}
	}
	// Clear unconditionally so a malformed cookie cannot persist.
	http.SetCookie(w, f.cookie("", -1))

	payloadPart, macPart, sep := strings.Cut(c.Value, ".")
	if !sep {
		return FlashRead{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return FlashRead{}
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return FlashRead{}
	}
	if !hmac.Equal(gotMAC, f.sign(payload)) {
		return FlashRead{}
	}

	return decodeFlash(payload)
}

func (f *EmailFlash) set(w http.ResponseWriter, kind FlashKind, msg string, wait int) {
	payload := encodeFlash(kind, msg, wait)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(f.sign(payload))
	http.SetCookie(w, f.cookie(value, flashMaxAge))
}

func (f *EmailFlash) cookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure follows cfg.SecureCookies() like the session cookie (#205).
	return &http.Cookie{
		Name:     emailFlashCookieName,
		Value:    value,
		Path:     "/admin",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   f.secureCookies,
		SameSite: http.SameSiteStrictMode,
	}
}

func (f *EmailFlash) sign(payload []byte) []byte {
	h := hmac.New(sha256.New, f.key)
	// hash.Hash.Write never returns an error.
	_, _ = h.Write(payload)

	return h.Sum(nil)
}

// encodeFlash returns the payload as kind-byte + optional
// wait-seconds + flashWaitSep + message.
func encodeFlash(kind FlashKind, msg string, wait int) []byte {
	var sb strings.Builder
	sb.WriteByte(byte(kind))
	if kind == FlashError && wait > 0 {
		sb.WriteString(strconv.Itoa(wait))
	}
	sb.WriteString(flashWaitSep)
	sb.WriteString(msg)

	return []byte(sb.String())
}

func decodeFlash(payload []byte) FlashRead {
	if len(payload) < 1 {
		return FlashRead{}
	}
	kind := FlashKind(payload[0])
	if kind != FlashNotice && kind != FlashError {
		return FlashRead{}
	}
	rest := string(payload[1:])
	waitPart, msg, sep := strings.Cut(rest, flashWaitSep)
	if !sep {
		return FlashRead{}
	}
	wait := 0
	if waitPart != "" {
		parsed, err := strconv.Atoi(waitPart)
		if err != nil || parsed < 0 {
			return FlashRead{}
		}
		wait = parsed
	}

	return FlashRead{Kind: kind, Msg: msg, Wait: wait, OK: true}
}
