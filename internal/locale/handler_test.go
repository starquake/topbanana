package locale_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/starquake/topbanana/internal/locale"
)

func TestHandleSetLocale(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		locale       string
		referer      string
		wantCookie   bool
		wantValue    string
		wantRedirect string
	}{
		{
			name:         "valid locale sets cookie and returns to referer",
			locale:       "nl",
			referer:      "https://example.test/quizzes",
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/quizzes",
		},
		{
			name:         "invalid locale sets no cookie",
			locale:       "fr",
			referer:      "https://example.test/",
			wantCookie:   false,
			wantRedirect: "/",
		},
		{name: "no referer redirects to root", locale: "en", wantCookie: true, wantValue: "en", wantRedirect: "/"},
		{
			name:         "referer query is preserved",
			locale:       "nl",
			referer:      "https://example.test/login?next=%2Fadmin",
			wantCookie:   true,
			wantValue:    "nl",
			wantRedirect: "/login?next=%2Fadmin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.Handle("GET /lang/{locale}", HandleSetLocale())

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lang/"+tc.locale, nil)
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
