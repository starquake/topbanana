package locale_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	. "github.com/starquake/topbanana/internal/locale"
)

func TestHandleSetLocale(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		locale       string
		query        string
		referer      string
		wantCookie   bool
		wantValue    string
		wantRedirect string
	}{
		{
			name:         "valid locale sets cookie and returns to next",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("/join/ABCD"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/join/ABCD",
		},
		{
			name:         "next query is preserved",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("/login?next=%2Fadmin"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/login?next=%2Fadmin",
		},
		{
			name:         "invalid locale sets no cookie",
			locale:       "fr",
			query:        "?next=%2Fquizzes",
			wantCookie:   false,
			wantRedirect: "/quizzes",
		},
		{name: "missing next redirects to root", locale: "en", wantCookie: true, wantValue: "en", wantRedirect: "/"},
		{
			name:         "empty next redirects to root",
			locale:       "en",
			query:        "?next=",
			wantCookie:   true,
			wantValue:    "en",
			wantRedirect: "/",
		},
		{
			name:         "referer is ignored",
			locale:       "nl",
			referer:      "https://example.test/quizzes",
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "protocol-relative next is rejected",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("//evil.example/x"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "absolute next is rejected",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("https://evil.example/"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "opaque next is rejected",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("javascript:alert(1)"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "non-rooted next is rejected",
			locale:       "nl",
			query:        "?next=quizzes",
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "backslash next is rejected",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape(`/\evil.example`),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
		{
			name:         "encoded backslash next is rejected",
			locale:       "nl",
			query:        "?next=" + url.QueryEscape("/%5cevil.example"),
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.Handle("GET /lang/{locale}", HandleSetLocale())

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lang/"+tc.locale+tc.query, nil)
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := rec.Header().Get("Location"), tc.wantRedirect; got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}

			var langCookie *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == CookieName {
					langCookie = c
				}
			}
			if tc.wantCookie {
				if langCookie == nil {
					t.Fatalf("expected a %q cookie, got none", CookieName)
				}
				if got, want := langCookie.Value, tc.wantValue; got != want {
					t.Errorf("cookie value = %q, want %q", got, want)
				}
			} else if langCookie != nil {
				t.Errorf("expected no %q cookie, got %q", CookieName, langCookie.Value)
			}
		})
	}
}

func TestSwitchHref(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		target string
		want   string
	}{
		{
			name:   "GET carries the path",
			method: http.MethodGet,
			target: "/join/ABCD",
			want:   "/lang/nl?next=%2Fjoin%2FABCD",
		},
		{
			name:   "GET carries the query",
			method: http.MethodGet,
			target: "/login?next=%2Fadmin",
			want:   "/lang/nl?next=%2Flogin%3Fnext%3D%252Fadmin",
		},
		{
			name:   "reset token page omits next",
			method: http.MethodGet,
			target: "/reset-password?token=secret",
			want:   "/lang/nl",
		},
		{
			name:   "verify token page omits next",
			method: http.MethodGet,
			target: "/verify-email?token=secret",
			want:   "/lang/nl",
		},
		{
			name:   "OAuth callback omits next",
			method: http.MethodGet,
			target: "/login/google/callback?code=c&state=s",
			want:   "/lang/nl",
		},
		{
			name:   "non-secret state filter keeps next",
			method: http.MethodGet,
			target: "/admin/players?state=active",
			want:   "/lang/nl?next=%2Fadmin%2Fplayers%3Fstate%3Dactive",
		},
		{name: "POST omits next", method: http.MethodPost, target: "/register", want: "/lang/nl"},
		{name: "HEAD omits next", method: http.MethodHead, target: "/join/ABCD", want: "/lang/nl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, nil)
			loc := "nl"
			if got, want := SwitchHref(r, loc), tc.want; got != want {
				t.Errorf("SwitchHref(%q %q, %q) = %q, want %q", tc.method, tc.target, loc, got, want)
			}
		})
	}
}
