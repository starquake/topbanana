package request_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/request"
)

func mustCIDRs(t *testing.T, raw string) []*net.IPNet {
	t.Helper()
	cidrs, err := ParseTrustedProxyCIDRs(raw)
	if err != nil {
		t.Fatalf("ParseTrustedProxyCIDRs(%q) err = %v, want nil", raw, err)
	}

	return cidrs
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns nil", func(t *testing.T) {
		t.Parallel()

		cidrs, err := ParseTrustedProxyCIDRs("")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got := cidrs; got != nil {
			t.Errorf("cidrs = %v, want nil", got)
		}
	})

	t.Run("single CIDR parses", func(t *testing.T) {
		t.Parallel()

		cidrs, err := ParseTrustedProxyCIDRs("127.0.0.1/32")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got, want := len(cidrs), 1; got != want {
			t.Fatalf("len(cidrs) = %d, want %d", got, want)
		}
	})

	t.Run("multiple CIDRs parse", func(t *testing.T) {
		t.Parallel()

		cidrs, err := ParseTrustedProxyCIDRs("10.0.0.0/8, 192.168.0.0/16 ,127.0.0.1/32")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got, want := len(cidrs), 3; got != want {
			t.Fatalf("len(cidrs) = %d, want %d", got, want)
		}
	})

	t.Run("empty entries are dropped", func(t *testing.T) {
		t.Parallel()

		cidrs, err := ParseTrustedProxyCIDRs(",10.0.0.0/8,,192.168.0.0/16,")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got, want := len(cidrs), 2; got != want {
			t.Errorf("len(cidrs) = %d, want %d", got, want)
		}
	})

	t.Run("only commas yields nil", func(t *testing.T) {
		t.Parallel()

		cidrs, err := ParseTrustedProxyCIDRs(", ,")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got := cidrs; got != nil {
			t.Errorf("cidrs = %v, want nil", got)
		}
	})

	t.Run("invalid CIDR returns wrapped error", func(t *testing.T) {
		t.Parallel()

		_, err := ParseTrustedProxyCIDRs("not-a-cidr")
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
		if got, want := err.Error(), "not-a-cidr"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("bare IP without prefix is rejected", func(t *testing.T) {
		t.Parallel()

		_, err := ParseTrustedProxyCIDRs("127.0.0.1")
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		trusted    string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "empty trust list ignores XFF",
			trusted:    "",
			remoteAddr: "127.0.0.1:12345",
			xff:        "1.2.3.4",
			want:       "127.0.0.1",
		},
		{
			name:       "empty trust list returns RemoteAddr host",
			trusted:    "",
			remoteAddr: "10.0.0.1:5555",
			want:       "10.0.0.1",
		},
		{
			name:       "trusted proxy with single XFF entry returns the entry",
			trusted:    "127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        "1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:       "untrusted peer ignores XFF",
			trusted:    "127.0.0.1/32",
			remoteAddr: "8.8.8.8:55555",
			xff:        "1.2.3.4",
			want:       "8.8.8.8",
		},
		{
			name:       "trusted peer but no XFF returns RemoteAddr host",
			trusted:    "127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        "",
			want:       "127.0.0.1",
		},
		{
			name:       "right-to-left walk skips trusted hops",
			trusted:    "10.0.0.0/8,127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        "1.2.3.4, 10.0.0.5, 10.0.0.6",
			want:       "1.2.3.4",
		},
		{
			name:       "rightmost untrusted entry wins on a deeper chain",
			trusted:    "127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        "9.9.9.9, 1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:       "all-trusted XFF falls back to leftmost entry",
			trusted:    "10.0.0.0/8,127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        "10.0.0.5, 10.0.0.6",
			want:       "10.0.0.5",
		},
		{
			name:       "unparseable RemoteAddr is returned verbatim with no trust",
			trusted:    "",
			remoteAddr: "missing-port",
			want:       "missing-port",
		},
		{
			name:       "unparseable RemoteAddr is treated as untrusted when a trust list is set",
			trusted:    "127.0.0.1/32",
			remoteAddr: "missing-port",
			xff:        "1.2.3.4",
			want:       "missing-port",
		},
		{
			name:       "ipv6 trusted peer walks XFF",
			trusted:    "::1/128",
			remoteAddr: "[::1]:55555",
			xff:        "1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:       "XFF with leading/trailing whitespace and empties is normalised",
			trusted:    "127.0.0.1/32",
			remoteAddr: "127.0.0.1:55555",
			xff:        ", 1.2.3.4 ,",
			want:       "1.2.3.4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/anything", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got, want := ClientIP(req, mustCIDRs(t, tt.trusted)), tt.want; got != want {
				t.Errorf("ClientIP = %q, want %q", got, want)
			}
		})
	}
}
