//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestFavicon_Integration covers ticket #236 — the banana SVG is served
// from /assets/banana.svg, and every layout (admin, auth, player)
// references it via <link rel="icon">. The SVG asset check guards
// against the file being moved or accidentally excluded from the
// embed; the per-layout HTML checks guard against any of the three
// base templates losing the link tag.
func TestFavicon_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	t.Run("svg served at /assets/banana.svg", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/assets/banana.svg", nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("Body.Close err = %v, want nil", cerr)
			}
		}()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "image/svg+xml"; !strings.HasPrefix(got, want) {
			t.Errorf("Content-Type = %q, want prefix %q", got, want)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll err = %v, want nil", err)
		}
		// Sanity check: the served bytes are actually an SVG carrying the
		// banana paths — not an empty file or a stray placeholder.
		if got, want := string(body), "<svg"; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
		if got, want := string(body), "M5.15 17.89"; !strings.Contains(got, want) {
			t.Errorf("body missing banana path %q", want)
		}
	})

	t.Run("auth login page links the favicon", func(t *testing.T) {
		t.Parallel()
		assertFaviconLink(ctx, t, http.DefaultClient, srv.BaseURL+"/login")
	})

	t.Run("player index links the favicon", func(t *testing.T) {
		t.Parallel()
		assertFaviconLink(ctx, t, http.DefaultClient, srv.BaseURL+"/client/")
	})

	t.Run("admin index links the favicon", func(t *testing.T) {
		t.Parallel()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar.New err = %v, want nil", err)
		}
		// CheckRedirect: ErrUseLastResponse so registerAdminViaHTTP sees the
		// 303 it expects (the default client would follow it). Once the
		// session cookie is set, GET /admin still returns 200 directly so
		// the redirect override doesn't affect the favicon check below.
		client := &http.Client{
			Jar: jar,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		registerAdminViaHTTP(ctx, t, client, srv.BaseURL)
		assertFaviconLink(ctx, t, client, srv.BaseURL+"/admin")
	})
}

func assertFaviconLink(ctx context.Context, t *testing.T, client *http.Client, url string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	}()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), `rel="icon"`; !strings.Contains(got, want) {
		t.Errorf("HTML missing favicon link (%q)", want)
	}
	if got, want := string(body), `/assets/banana.svg`; !strings.Contains(got, want) {
		t.Errorf("HTML missing favicon href (%q)", want)
	}
}
