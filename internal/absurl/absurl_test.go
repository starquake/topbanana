package absurl_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/absurl"
)

func TestBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		host    string
		tls     bool
		headers map[string]string
		want    string
	}{
		{
			name: "plain http with host",
			host: "example.com",
			want: "http://example.com",
		},
		{
			name: "TLS adds https scheme",
			host: "example.com",
			tls:  true,
			want: "https://example.com",
		},
		{
			name:    "X-Forwarded-Proto overrides scheme",
			host:    "example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "https://example.com",
		},
		{
			name:    "X-Forwarded-Host overrides host",
			host:    "internal:8080",
			headers: map[string]string{"X-Forwarded-Host": "topbanana.example.com"},
			want:    "http://topbanana.example.com",
		},
		{
			name: "proxy chain uses first hop values",
			host: "internal:8080",
			headers: map[string]string{
				"X-Forwarded-Proto": "https, http",
				"X-Forwarded-Host":  "topbanana.example.com, internal.lb",
			},
			want: "https://topbanana.example.com",
		},
		{
			name:    "whitespace around first hop value is trimmed",
			host:    "internal:8080",
			headers: map[string]string{"X-Forwarded-Host": "  topbanana.example.com  , internal.lb"},
			want:    "http://topbanana.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+tc.host+"/", nil)
			req.Host = tc.host
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if got, want := absurl.BaseURL(req), tc.want; got != want {
				t.Errorf("BaseURL = %q, want %q", got, want)
			}
		})
	}
}
