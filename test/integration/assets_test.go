package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAssets_Integration guards the admin/auth asset pipeline end to end:
// the route at /static/ is mounted, the embedded FS sees the generated
// app.css, and the file contains utilities that prove Tailwind scanned
// the right templates. If anyone moves the source file or breaks the
// @source paths in frontend/web/css/tailwind.css, the regenerated CSS would still serve
// (200 OK) but lose its custom utilities — only a class-presence check
// catches that.
func TestAssets_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/static/css/app.css", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to GET app.css: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("failed to close body: %v", cerr)
		}
	}()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "text/css"; !strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, want prefix %q", got, want)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	css := string(body)

	// If @source paths are broken, Tailwind emits preflight + theme only —
	// well under 5KB. Real output with utilities sits around 30KB.
	if got, want := len(css), 10_000; got < want {
		t.Errorf("app.css length = %d bytes, want > %d (Tailwind likely scanned no templates)", got, want)
	}

	// Each class below requires both a working theme token AND a template
	// reference to be emitted. Together they prove the pipeline scanned
	// internal/web/tmpl/{admin,auth} and consumed the @theme block.
	wantClasses := []string{
		"max-w-shell",   // custom --container-shell token + used in base layouts
		"bg-bg",         // custom --color-bg token + used on <body>
		"font-display",  // custom --font-display token + used in headings
		"btn-primary",   // @apply component class
		"border-dashed", // arbitrary utility used in the empty-state card
		"shadow-focus",  // custom --shadow-focus token + focus-visible: variants
		"q-row",         // quizview's question card pattern
		"option-row",    // questionform's option editor pattern
		"pill-public",   // visibility pill (currently hardcoded; #103)
	}
	var missing []string
	for _, cls := range wantClasses {
		if !strings.Contains(css, cls) {
			missing = append(missing, cls)
		}
	}
	if len(missing) > 0 {
		t.Errorf(
			"app.css missing %d expected classes %v (Tailwind didn't scan templates or theme tokens missing)",
			len(missing),
			missing,
		)
	}
}
