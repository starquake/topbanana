package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/web"
)

// TestHandler_DefaultServesEmbeddedFS pins the production default: with
// WebStaticDir empty, Handler serves the [embed.FS] tree so the binary
// stays self-contained.
func TestHandler_DefaultServesEmbeddedFS(t *testing.T) {
	t.Parallel()

	h := web.Handler(&config.Config{AppEnvironment: "development"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/css/app.css", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (embedded /assets/css/app.css should always be present)", got, want)
	}
	if rr.Body.Len() == 0 {
		t.Error("body is empty; expected the embedded Tailwind output")
	}
}

// TestHandler_WebStaticDirServesOnDisk pins the dev override: a file
// written to WebStaticDir is the one Handler serves, not the embedded
// version. The on-disk content is a sentinel string that cannot appear
// in the committed Tailwind output, so the assertion can't be fooled
// by an accidental embed-FS hit.
func TestHandler_WebStaticDirServesOnDisk(t *testing.T) {
	t.Parallel()

	staticDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticDir, "css"), 0o755); err != nil {
		t.Fatalf("MkdirAll err = %v, want nil", err)
	}
	sentinel := "/* web-static-dir-override-sentinel */"
	if err := os.WriteFile(filepath.Join(staticDir, "css", "app.css"), []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile err = %v, want nil", err)
	}

	h := web.Handler(&config.Config{AppEnvironment: "development", WebStaticDir: staticDir})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/css/app.css", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), sentinel; !strings.Contains(got, want) {
		t.Errorf("body should contain sentinel %q (override not honoured)", want)
	}
}
