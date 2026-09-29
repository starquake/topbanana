package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

// flashCookie sets a notice on flash and returns the cookie it wrote.
func flashCookie(t *testing.T, flash *SignedFlash, msg string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	flash.SetNotice(rec, msg)
	cookies := rec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("cookies = %d, want %d", got, want)
	}

	return cookies[0]
}

// readFlash reads flash back from a request carrying cookie.
func readFlash(t *testing.T, flash *SignedFlash, cookie *http.Cookie) SignedFlashRead {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	return flash.Read(httptest.NewRecorder(), req)
}

func TestSignedFlash_RoundTrip(t *testing.T) {
	t.Parallel()

	flash := NewSignedFlash([]byte("session-key"), false, "tb_flash", "/")
	got := readFlash(t, flash, flashCookie(t, flash, "saved"))

	if !got.OK {
		t.Fatal("Read OK = false, want true")
	}
	if got, want := got.Notice, "saved"; got != want {
		t.Errorf("Notice = %q, want %q", got, want)
	}
}

// TestSignedFlash_RejectsRawSessionKeyMAC pins #1374: the flash is signed with
// a key derived from SESSION_KEY, not SESSION_KEY itself, so a value MAC'd
// with the raw key is rejected.
func TestSignedFlash_RejectsRawSessionKeyMAC(t *testing.T) {
	t.Parallel()

	sessionKey := []byte("session-key")
	flash := NewSignedFlash(sessionKey, false, "tb_flash", "/")
	cookie := flashCookie(t, flash, "saved")

	payloadPart, _, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		t.Fatalf("cookie value %q has no MAC separator", cookie.Value)
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		t.Fatalf("DecodeString err = %v, want nil", err)
	}
	h := hmac.New(sha256.New, sessionKey)
	_, _ = h.Write(payload)
	cookie.Value = payloadPart + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if got := readFlash(t, flash, cookie); got.OK {
		t.Errorf("Read OK = true for a raw SESSION_KEY MAC, want false (Notice=%q)", got.Notice)
	}
}
