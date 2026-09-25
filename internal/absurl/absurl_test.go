package absurl_test

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/starquake/topbanana/internal/absurl"
)

func mustCIDR(t *testing.T, cidr string) []*net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q) err = %v, want nil", cidr, err)
	}

	return []*net.IPNet{n}
}

func TestBaseURL(t *testing.T) {
	t.Parallel()

	const trustedPeer = "10.0.0.2:4000"
	const clientPeer = "203.0.113.9:4000"
	forged := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "attacker.example"}

	tests := []struct {
		name       string
		middleware bool
		configured string
		remoteAddr string
		host       string
		tls        bool
		headers    map[string]string
		want       string
	}{
		{name: "plain http with host", host: "example.com", want: "http://example.com"},
		{name: "TLS adds https scheme", host: "example.com", tls: true, want: "https://example.com"},
		{
			name: "without the middleware forwarded headers are ignored",
			host: "example.com", headers: forged, want: "http://example.com",
		},
		{
			name: "configured base URL wins over the request", middleware: true,
			configured: "https://quiz.example/", remoteAddr: trustedPeer, host: "internal:8080",
			headers: forged, want: "https://quiz.example",
		},
		{
			name: "untrusted peer cannot set the host", middleware: true,
			remoteAddr: clientPeer, host: "example.com", headers: forged, want: "http://example.com",
		},
		{
			name: "trusted proxy sets scheme and host", middleware: true,
			remoteAddr: trustedPeer, host: "internal:8080",
			headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "quiz.example"},
			want:    "https://quiz.example",
		},
		{
			name: "trusted proxy that appends keeps its own hop, not the client's", middleware: true,
			remoteAddr: trustedPeer, host: "internal:8080",
			headers: map[string]string{
				"X-Forwarded-Proto": "http, https",
				"X-Forwarded-Host":  "attacker.example,  quiz.example  ",
			},
			want: "https://quiz.example",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+tc.host+"/", nil)
			req.Host = tc.host
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			var got string
			capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = BaseURL(r) })
			if tc.middleware {
				Middleware(tc.configured, mustCIDR(t, "10.0.0.0/8"))(capture).ServeHTTP(httptest.NewRecorder(), req)
			} else {
				capture.ServeHTTP(httptest.NewRecorder(), req)
			}

			if want := tc.want; got != want {
				t.Errorf("BaseURL = %q, want %q", got, want)
			}
		})
	}
}
