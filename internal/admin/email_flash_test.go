package admin_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
)

// TestEmailFlash_RejectsRawSessionKeyMAC pins that the flash is signed with a
// key derived from SESSION_KEY: a cookie MAC'd with the raw key (as issued
// before the derivation) reads as no banner and is cleared.
func TestEmailFlash_RejectsRawSessionKeyMAC(t *testing.T) {
	t.Parallel()

	sessionKey := []byte("session-key")
	flash := NewEmailFlash(sessionKey, false)
	setRec := httptest.NewRecorder()
	flash.SetNotice(setRec, "sent", "to@example.test")
	cookies := setRec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("cookies = %d, want %d", got, want)
	}
	cookie := cookies[0]

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
	oldKeyCookie := &http.Cookie{
		Name:  cookie.Name,
		Value: payloadPart + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)),
	}

	validReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/email", nil)
	validReq.AddCookie(cookie)
	if got := flash.Read(httptest.NewRecorder(), validReq); !got.OK {
		t.Fatal("Read OK = false for a freshly signed cookie, want true")
	}

	readRec := httptest.NewRecorder()
	oldReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/email", nil)
	oldReq.AddCookie(oldKeyCookie)
	if got := flash.Read(readRec, oldReq); got.OK {
		t.Errorf("Read OK = true for a raw SESSION_KEY MAC, want false (Msg=%q)", got.Msg)
	}
	cleared := readRec.Result().Cookies()
	if got, want := len(cleared), 1; got != want {
		t.Fatalf("Read Set-Cookie count = %d, want %d (the rejected cookie is cleared)", got, want)
	}
	if got, want := cleared[0].MaxAge, -1; got != want {
		t.Errorf("cleared cookie MaxAge = %d, want %d", got, want)
	}
}
