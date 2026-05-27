package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// TestGoogleNextCookie_RoundTrip pins the OAuth `next`-cookie wire
// format end-to-end: sign a path with the deployment key, attach the
// resulting cookie to a request, and check the same path comes out
// the other side. This is the contract HandleGoogleLogin and
// HandleGoogleCallback rely on across the round trip to Google (#449).
func TestGoogleNextCookie_RoundTrip(t *testing.T) {
	t.Parallel()

	key := []byte("test-key-32-bytes-test-key-32byt")
	signed := auth.ExportSignNext(key, "/admin/email")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/google/callback", nil)
	req.AddCookie(&http.Cookie{Name: auth.GoogleNextCookieName, Value: signed})

	if got, want := auth.ExportReadGoogleNext(req, key), "/admin/email"; got != want {
		t.Errorf("ExportReadGoogleNext = %q, want %q", got, want)
	}
}

// TestGoogleNextCookie_BadSignatureRejected pins the HMAC gate: a
// cookie with a tampered MAC must not yield a usable path.
func TestGoogleNextCookie_BadSignatureRejected(t *testing.T) {
	t.Parallel()

	signedWithOtherKey := auth.ExportSignNext([]byte("attacker-key-xxxxxxxxxxxxxxxxxxx"), "/admin/email")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/google/callback", nil)
	req.AddCookie(&http.Cookie{Name: auth.GoogleNextCookieName, Value: signedWithOtherKey})

	deploymentKey := []byte("real-deployment-key-xxxxxxxxxxxx")
	if got, want := auth.ExportReadGoogleNext(req, deploymentKey), ""; got != want {
		t.Errorf("ExportReadGoogleNext under bad-signature = %q, want %q", got, want)
	}
}

// TestGoogleNextCookie_MissingCookie pins the no-next default: a
// request with no cookie returns the empty string so the callback
// falls back to the role landing.
func TestGoogleNextCookie_MissingCookie(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/google/callback", nil)
	if got, want := auth.ExportReadGoogleNext(req, []byte("k")), ""; got != want {
		t.Errorf("ExportReadGoogleNext with no cookie = %q, want %q", got, want)
	}
}

// TestGoogleNextCookie_UnsafePathRejected pins that a signed-but-unsafe
// path (e.g. signed before SafeNextPath tightened) does not leak
// through. The signature gate doesn't bind us to forwarding the
// payload; SafeNextPath re-runs on read.
func TestGoogleNextCookie_UnsafePathRejected(t *testing.T) {
	t.Parallel()

	key := []byte("test-key-32-bytes-test-key-32byt")
	// Sign an attacker-controlled URL with the real deployment key
	// (simulates a stale cookie if the validator was later tightened).
	signed := auth.ExportSignNext(key, "//evil.com/")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/google/callback", nil)
	req.AddCookie(&http.Cookie{Name: auth.GoogleNextCookieName, Value: signed})

	if got, want := auth.ExportReadGoogleNext(req, key), ""; got != want {
		t.Errorf("ExportReadGoogleNext for unsafe path = %q, want %q", got, want)
	}
}
