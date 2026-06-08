package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/starquake/topbanana/internal/server"
)

func TestOriginFromBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "empty", baseURL: "", want: ""},
		{name: "scheme and host", baseURL: "https://quiz.example.com", want: "https://quiz.example.com"},
		{name: "drops path", baseURL: "https://quiz.example.com/app/", want: "https://quiz.example.com"},
		{name: "lowercases", baseURL: "HTTPS://Quiz.Example.COM", want: "https://quiz.example.com"},
		{name: "keeps port", baseURL: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "host only no scheme", baseURL: "quiz.example.com", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := ExportOriginFromBaseURL(tc.baseURL), tc.want; got != want {
				t.Errorf("originFromBaseURL(%q) = %q, want %q", tc.baseURL, got, want)
			}
		})
	}
}

func TestSameOriginCheck(t *testing.T) {
	t.Parallel()

	const expectedOrigin = "https://quiz.example.com"

	tests := []struct {
		name           string
		method         string
		host           string
		expectedOrigin string
		secFetchSite   string
		origin         string
		wantStatus     int
	}{
		{
			name:           "safe GET passes regardless of origin",
			method:         http.MethodGet,
			expectedOrigin: expectedOrigin,
			origin:         "https://evil.example.com",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "sec-fetch-site same-origin allowed",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			secFetchSite:   "same-origin",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "sec-fetch-site same-site allowed",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			secFetchSite:   "same-site",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "sec-fetch-site cross-site rejected",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			secFetchSite:   "cross-site",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:           "sec-fetch-site none rejected",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			secFetchSite:   "none",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:           "sec-fetch-site wins over mismatched origin",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			secFetchSite:   "same-origin",
			origin:         "https://evil.example.com",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "matching origin allowed",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			origin:         "https://quiz.example.com",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "matching origin case-insensitive",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			origin:         "https://Quiz.Example.com",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "cross-site origin rejected",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			origin:         "https://evil.example.com",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:           "scheme mismatch rejected",
			method:         http.MethodPost,
			expectedOrigin: expectedOrigin,
			origin:         "http://quiz.example.com",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:       "no headers allowed (non-browser client)",
			method:     http.MethodPost,
			wantStatus: http.StatusOK,
		},
		{
			name:       "fallback to request host: matching allowed",
			method:     http.MethodPost,
			host:       "lan-host:8080",
			origin:     "http://lan-host:8080",
			wantStatus: http.StatusOK,
		},
		{
			name:       "fallback to request host: mismatch rejected",
			method:     http.MethodPost,
			host:       "lan-host:8080",
			origin:     "http://other-host:8080",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "delete with cross-site rejected",
			method:     http.MethodDelete,
			origin:     "https://evil.example.com",
			host:       "lan-host:8080",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reached bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/api/players/me", nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}

			rec := httptest.NewRecorder()
			ExportSameOriginCheck(tc.expectedOrigin, next).ServeHTTP(rec, req)

			if got, want := rec.Code, tc.wantStatus; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := reached, tc.wantStatus == http.StatusOK; got != want {
				t.Errorf("next reached = %t, want %t", got, want)
			}
		})
	}
}
