package qrcode_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/qrcode"
)

// TestSVG pins the matrix -> SVG rendering: a standalone <svg> document with a
// full-bleed white background, a square viewBox sized in module units, and one
// "h1v1h-1z" path command per dark module. The QR encoder's exact module
// layout and chosen version are the library's concern, so the assertions check
// the document shape and that the viewBox is square and at least a version-1
// symbol plus its quiet zone (21 + 2*4 = 29 modules) rather than an exact count.
func TestSVG(t *testing.T) {
	t.Parallel()

	svg, err := qrcode.SVG([]byte("https://example.org/join/ABCD"))
	if err != nil {
		t.Fatalf("SVG err = %v, want nil", err)
	}

	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Errorf("svg = %q, want a standalone <svg> document", svg)
	}
	if got := strings.Count(svg, "h1v1h-1z"); got <= 0 {
		t.Errorf("dark module count = %d, want > 0", got)
	}

	var w, h int
	if _, err := fmt.Sscanf(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d"`, &w, &h); err != nil {
		t.Fatalf("could not parse viewBox from %q: %v", svg, err)
	}
	if w != h {
		t.Errorf("viewBox = %dx%d, want square", w, h)
	}
	if w < 29 {
		t.Errorf("viewBox dimension = %d, want at least 29", w)
	}
	if got, want := svg, fmt.Sprintf(
		`<rect width="%d" height="%d" fill="#ffffff"/>`,
		w,
		h,
	); !strings.Contains(
		got,
		want,
	) {
		t.Errorf("svg = %q, should contain %q", got, want)
	}
}
