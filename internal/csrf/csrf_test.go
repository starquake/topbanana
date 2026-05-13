package csrf_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/csrf"
)

// newGetRequest builds a GET request with the test context attached so
// httptest plays nicely with t.Cleanup.
func newGetRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
}

// newPostFormRequest builds a POST request with form data and the test context.
// All tests in this file post to the same path; the choice is arbitrary
// because the middleware is path-agnostic.
func newPostFormRequest(t *testing.T, values url.Values) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/admin/quizzes",
		strings.NewReader(values.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

// nonceCookieFromResponse returns the value of the CSRF nonce cookie set on
// the response, or an empty string if no such cookie was set.
func nonceCookieFromResponse(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrf.CookieName {
			return c.Value
		}
	}

	return ""
}

func TestToken_SetsCookieWhenAbsent(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")

	tok := m.Token(rec, req)
	if got := tok; got == "" {
		t.Fatal("Token returned empty string, want non-empty")
	}

	nonce := nonceCookieFromResponse(rec)
	if nonce == "" {
		t.Fatalf("expected %q cookie to be set, got none", csrf.CookieName)
	}
}

func TestToken_CookieFlags_RespectSecureCookies(t *testing.T) {
	t.Parallel()
	// secureCookies=true (production) must set the Secure attribute;
	// secureCookies=false (development) must drop it so browsers accept
	// the cookie over plain HTTP from LAN hostnames — see #205. The
	// HttpOnly + SameSite=Lax flags stay on regardless of mode.

	t.Run("production sets Secure", func(t *testing.T) {
		t.Parallel()
		m := csrf.New([]byte("test-key"), true)
		rec := httptest.NewRecorder()
		_ = m.Token(rec, newGetRequest(t, "/"))

		c := rec.Result().Cookies()[0]
		if !c.Secure {
			t.Error("cookie Secure = false, want true in production")
		}
		if !c.HttpOnly {
			t.Error("cookie HttpOnly = false, want true")
		}
		if got, want := c.SameSite, http.SameSiteLaxMode; got != want {
			t.Errorf("cookie SameSite = %v, want %v", got, want)
		}
	})

	t.Run("development drops Secure", func(t *testing.T) {
		t.Parallel()
		m := csrf.New([]byte("test-key"), false)
		rec := httptest.NewRecorder()
		_ = m.Token(rec, newGetRequest(t, "/"))

		c := rec.Result().Cookies()[0]
		if c.Secure {
			t.Error("cookie Secure = true, want false in dev")
		}
		// HttpOnly and SameSite must still be set even in dev — only
		// the Secure attribute is environment-gated.
		if !c.HttpOnly {
			t.Error("cookie HttpOnly = false, want true even in dev")
		}
		if got, want := c.SameSite, http.SameSiteLaxMode; got != want {
			t.Errorf("cookie SameSite = %v, want %v even in dev", got, want)
		}
	})
}

func TestToken_ReusesExistingCookie(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	// Issue a nonce first so we have a stable cookie value.
	rec1 := httptest.NewRecorder()
	req1 := newGetRequest(t, "/login")
	tok1 := m.Token(rec1, req1)

	nonce := nonceCookieFromResponse(rec1)
	if nonce == "" {
		t.Fatalf("expected %q cookie to be set on first call", csrf.CookieName)
	}

	// Second request with the cookie attached should reuse it.
	rec2 := httptest.NewRecorder()
	req2 := newGetRequest(t, "/login")
	req2.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: nonce})

	tok2 := m.Token(rec2, req2)

	if got, want := tok2, tok1; got != want {
		t.Errorf("Token = %q, want %q (should be deterministic for same nonce)", got, want)
	}
	if got := nonceCookieFromResponse(rec2); got != "" {
		t.Errorf("expected no new Set-Cookie when nonce already present, got %q", got)
	}
}

func TestToken_DeterministicForSameNonce(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "fixed-nonce-value"})

	t1 := m.Token(rec, req)
	t2 := m.Token(rec, req)

	if got, want := t2, t1; got != want {
		t.Errorf("Token = %q, want %q (should be deterministic for same nonce)", got, want)
	}
}

func TestValidate_NoCookie(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)
	req := newPostFormRequest(t, url.Values{csrf.FormField: {"anything"}})

	if got, want := m.Validate(req), csrf.ErrInvalidToken; !errors.Is(got, want) {
		t.Errorf("Validate err = %v, want %v", got, want)
	}
}

func TestValidate_NoFormField(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)
	req := newPostFormRequest(t, url.Values{})
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "some-nonce"})

	if got, want := m.Validate(req), csrf.ErrInvalidToken; !errors.Is(got, want) {
		t.Errorf("Validate err = %v, want %v", got, want)
	}
}

func TestValidate_TamperedToken(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	// Get a valid token.
	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "real-nonce"})
	valid := m.Token(rec, req)

	// Flip the last character to produce something that is still
	// base64url-decodable but does not match the HMAC.
	last := valid[len(valid)-1]
	flipped := byte('A')
	if last == 'A' {
		flipped = 'B'
	}
	tampered := valid[:len(valid)-1] + string(flipped)

	postReq := newPostFormRequest(t, url.Values{csrf.FormField: {tampered}})
	postReq.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "real-nonce"})

	if got, want := m.Validate(postReq), csrf.ErrInvalidToken; !errors.Is(got, want) {
		t.Errorf("Validate err = %v, want %v", got, want)
	}
}

func TestValidate_DifferentKey(t *testing.T) {
	t.Parallel()

	managerA := csrf.New([]byte("key-a"), true)
	managerB := csrf.New([]byte("key-b"), true)

	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "shared-nonce"})
	tokenFromA := managerA.Token(rec, req)

	postReq := newPostFormRequest(t, url.Values{csrf.FormField: {tokenFromA}})
	postReq.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "shared-nonce"})

	if got, want := managerB.Validate(postReq), csrf.ErrInvalidToken; !errors.Is(got, want) {
		t.Errorf("Validate err = %v, want %v", got, want)
	}
}

func TestValidate_ValidToken(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "happy-path-nonce"})
	tok := m.Token(rec, req)

	postReq := newPostFormRequest(t, url.Values{csrf.FormField: {tok}})
	postReq.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "happy-path-nonce"})

	if err := m.Validate(postReq); err != nil {
		t.Errorf("Validate err = %v, want nil", err)
	}
}

func TestMiddleware_GET_PassesThrough(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})
	handler := m.Middleware(next)

	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/admin/quizzes")
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not invoked for GET request")
	}
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestMiddleware_POST_NoToken_403(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})
	handler := m.Middleware(next)

	rec := httptest.NewRecorder()
	req := newPostFormRequest(t, url.Values{})
	handler.ServeHTTP(rec, req)

	if called {
		t.Error("next handler was invoked despite missing CSRF token")
	}
	if got, want := rec.Code, http.StatusForbidden; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestMiddleware_POST_ValidToken_CallsNext(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	// Issue a token first so we know the nonce/token pair is valid.
	rec := httptest.NewRecorder()
	req := newGetRequest(t, "/login")
	req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "post-nonce"})
	tok := m.Token(rec, req)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := m.Middleware(next)

	postRec := httptest.NewRecorder()
	postReq := newPostFormRequest(t, url.Values{csrf.FormField: {tok}})
	postReq.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: "post-nonce"})
	handler.ServeHTTP(postRec, postReq)

	if !called {
		t.Error("next handler was not invoked despite valid CSRF token")
	}
	if got, want := postRec.Code, http.StatusNoContent; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestMiddleware_UnsafeMethods_AreValidated(t *testing.T) {
	t.Parallel()

	m := csrf.New([]byte("test-key"), true)

	tests := []struct {
		name   string
		method string
	}{
		{name: "POST", method: http.MethodPost},
		{name: "PUT", method: http.MethodPut},
		{name: "PATCH", method: http.MethodPatch},
		{name: "DELETE", method: http.MethodDelete},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			called := false
			handler := m.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				called = true
			}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/admin/quizzes", nil)
			handler.ServeHTTP(rec, req)

			if called {
				t.Errorf("%s: next handler was invoked despite missing CSRF token", tc.method)
			}
			if got, want := rec.Code, http.StatusForbidden; got != want {
				t.Errorf("%s: status = %d, want %d", tc.method, got, want)
			}
		})
	}
}
