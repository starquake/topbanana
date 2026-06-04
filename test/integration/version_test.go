package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestVersionEndpoint_Integration pins the public /version JSON shape and
// that it reports the configured APP_ENV. The build stamp is empty under
// `go test` (no ldflags), so the test asserts the structure plus the env
// rather than a stamped version string.
func TestVersionEndpoint_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/version", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to GET /version: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("failed to close response body: %v", cerr)
		}
	}()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	var body struct {
		Env     string `json:"env"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode /version body: %v", err)
	}

	// startServer boots with APP_ENV=development.
	if got, want := body.Env, "development"; got != want {
		t.Errorf("env = %q, want %q", got, want)
	}
	// Un-stamped build: Version falls back to "dev".
	if got, want := body.Version, "dev"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	// Commit is resolved from ReadBuildInfo under go test; it must not be
	// blank (it is at least "unknown").
	if body.Commit == "" {
		t.Errorf("commit = %q, want non-empty", body.Commit)
	}
}

// TestAdminFooterShowsVersion_Integration drives a real admin page and
// asserts the build-stamp footer renders the per-environment label. The
// harness runs as development, so the footer shows "development (<commit>)".
func TestAdminFooterShowsVersion_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "version-admin", "version-pass-123")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/quizzes", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to GET /admin/quizzes: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("failed to close response body: %v", cerr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	// The build-stamp footer renders the per-environment label; the harness
	// runs as development, so it reads "development (<commit>)" (#663).
	// Asserting the label (not the footer's CSS classes) keeps this robust
	// to restyling.
	if got, want := string(body), "development ("; !strings.Contains(got, want) {
		t.Errorf("admin page should contain version label prefix %q, body=%q", want, got)
	}
}
