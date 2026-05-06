package session_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/session"
)

// newManagerAt returns a Manager whose clock is fixed at the given time.
// Used by clock-sensitive tests that need deterministic issuedAt and expiry.
func newManagerAt(t *testing.T, when time.Time) *session.Manager {
	t.Helper()

	return session.ExportNewWithClock([]byte("k"), false, func() time.Time { return when })
}

func TestSet_AndPlayerID_RoundTrip(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("test-key"), false)
	rec := httptest.NewRecorder()
	mgr.Set(rec, 42)

	cookies := rec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("Set wrote %d cookies, want %d", got, want)
	}

	c := cookies[0]
	if got, want := c.Name, session.CookieName; got != want {
		t.Errorf("cookie name = %q, want %q", got, want)
	}
	if !c.HttpOnly {
		t.Error("cookie HttpOnly = false, want true")
	}
	if c.Secure {
		t.Error("cookie Secure = true, want false (secure=false was passed)")
	}
	if got, want := c.SameSite, http.SameSiteLaxMode; got != want {
		t.Errorf("cookie SameSite = %v, want %v", got, want)
	}
	if got, want := c.Path, "/"; got != want {
		t.Errorf("cookie Path = %q, want %q", got, want)
	}
	if got, want := c.MaxAge, session.MaxAge; got != want {
		t.Errorf("cookie MaxAge = %d, want %d", got, want)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(c)
	playerID, ok := mgr.PlayerID(req)
	if !ok {
		t.Fatal("PlayerID ok = false, want true")
	}
	if got, want := playerID, int64(42); got != want {
		t.Errorf("PlayerID = %d, want %d", got, want)
	}
}

func TestSet_SecureFlag(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), true)
	rec := httptest.NewRecorder()
	mgr.Set(rec, 1)

	cookies := rec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("Set wrote %d cookies, want %d", got, want)
	}
	if !cookies[0].Secure {
		t.Error("cookie Secure = false, want true (secure=true was passed)")
	}
}

func TestPlayerID_MissingCookie(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	_, ok := mgr.PlayerID(req)
	if ok {
		t.Error("PlayerID ok = true, want false")
	}
}

func TestPlayerID_TamperedSignature(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)
	rec := httptest.NewRecorder()
	mgr.Set(rec, 7)

	c := rec.Result().Cookies()[0]
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected cookie value %q", c.Value)
	}
	tampered := &http.Cookie{Name: c.Name, Value: parts[0] + ".AAAA"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(tampered)

	if _, ok := mgr.PlayerID(req); ok {
		t.Error("PlayerID ok = true, want false for tampered signature")
	}
}

func TestPlayerID_TamperedPayload(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)
	rec := httptest.NewRecorder()
	mgr.Set(rec, 7)

	c := rec.Result().Cookies()[0]
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected cookie value %q", c.Value)
	}
	// Replace payload with a different but well-formed value while keeping the original signature.
	fakePayload := base64.RawURLEncoding.EncodeToString([]byte("8|" + parts[0]))
	tampered := &http.Cookie{Name: c.Name, Value: fakePayload + "." + parts[1]}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(tampered)

	if _, ok := mgr.PlayerID(req); ok {
		t.Error("PlayerID ok = true, want false for tampered payload")
	}
}

func TestPlayerID_TamperedTimestamp(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)
	rec := httptest.NewRecorder()
	mgr.Set(rec, 7)

	c := rec.Result().Cookies()[0]
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected cookie value %q", c.Value)
	}
	// Replace the timestamp with a future value but keep the original signature.
	// MAC was computed over the original payload, so this should fail verification.
	fakePayload := base64.RawURLEncoding.EncodeToString([]byte("7|9999999999"))
	tampered := &http.Cookie{Name: c.Name, Value: fakePayload + "." + parts[1]}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(tampered)

	if _, ok := mgr.PlayerID(req); ok {
		t.Error("PlayerID ok = true, want false for tampered timestamp")
	}
}

func TestPlayerID_MalformedCookie(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)

	// base64url("not-an-int|123") and base64url("123|not-an-int") for the parse-error paths.
	badPlayerID := base64.RawURLEncoding.EncodeToString([]byte("not-an-int|123"))
	badIssuedAt := base64.RawURLEncoding.EncodeToString([]byte("123|not-an-int"))
	noSeparator := base64.RawURLEncoding.EncodeToString([]byte("123"))

	tests := []string{
		"",                    // empty
		"only-one-part",       // no dot
		"!!notbase64!!.AAAA",  // bad base64 in payload
		"OA.notbase64!!",      // bad base64 in mac
		noSeparator + ".AAAA", // payload missing the | separator
		badPlayerID + ".AAAA", // payload has non-integer player ID
		badIssuedAt + ".AAAA", // payload has non-integer issued-at
	}
	for _, value := range tests {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: value})

		if _, ok := mgr.PlayerID(req); ok {
			t.Errorf("PlayerID(%q) ok = true, want false", value)
		}
	}
}

func TestPlayerID_DifferentKey(t *testing.T) {
	t.Parallel()

	signer := session.New([]byte("real-key"), false)
	rec := httptest.NewRecorder()
	signer.Set(rec, 1)

	verifier := session.New([]byte("other-key"), false)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])

	if _, ok := verifier.PlayerID(req); ok {
		t.Error("PlayerID with wrong key ok = true, want false")
	}
}

func TestPlayerID_ExpiredCookie(t *testing.T) {
	t.Parallel()

	issuedAt := time.Unix(1_000_000, 0)

	signer := newManagerAt(t, issuedAt)
	rec := httptest.NewRecorder()
	signer.Set(rec, 5)
	c := rec.Result().Cookies()[0]

	// Advance past MaxAge by 1 second.
	verifier := newManagerAt(t, issuedAt.Add(time.Duration(session.MaxAge+1)*time.Second))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(c)

	if _, ok := verifier.PlayerID(req); ok {
		t.Error("PlayerID for expired cookie ok = true, want false")
	}
}

func TestPlayerID_BoundaryAgeIsValid(t *testing.T) {
	t.Parallel()

	// "Expired" is strictly older than MaxAge, so age == MaxAge is still valid.
	issuedAt := time.Unix(1_000_000, 0)

	signer := newManagerAt(t, issuedAt)
	rec := httptest.NewRecorder()
	signer.Set(rec, 5)
	c := rec.Result().Cookies()[0]

	verifier := newManagerAt(t, issuedAt.Add(time.Duration(session.MaxAge)*time.Second))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(c)

	playerID, ok := verifier.PlayerID(req)
	if !ok {
		t.Fatal("PlayerID at boundary ok = false, want true")
	}
	if got, want := playerID, int64(5); got != want {
		t.Errorf("PlayerID = %d, want %d", got, want)
	}
}

func TestClear(t *testing.T) {
	t.Parallel()

	mgr := session.New([]byte("k"), false)
	rec := httptest.NewRecorder()
	mgr.Clear(rec)

	cookies := rec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("Clear wrote %d cookies, want %d", got, want)
	}

	c := cookies[0]
	if got, want := c.Name, session.CookieName; got != want {
		t.Errorf("cookie name = %q, want %q", got, want)
	}
	if got, want := c.MaxAge, -1; got != want {
		t.Errorf("cookie MaxAge = %d, want %d", got, want)
	}
	if got, want := c.Value, ""; got != want {
		t.Errorf("cookie Value = %q, want %q", got, want)
	}
}
