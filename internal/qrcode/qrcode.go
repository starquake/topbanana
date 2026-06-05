// Package qrcode renders a QR code as a standalone SVG document. The QR
// encoding (matrix, masking, error correction) is delegated to
// github.com/skip2/go-qrcode; this package only turns the resulting module
// bitmap into crisp vector markup, which the host TV lobby needs so the code
// stays sharp at any display size (a raster PNG would blur when scaled).
package qrcode

import (
	"fmt"
	"strings"

	goqr "github.com/skip2/go-qrcode"
)

// SVG encodes data as a QR code and returns a standalone SVG document. The
// viewBox is in module units (1 module = 1 unit) and includes the QR spec's
// quiet zone, so the caller sizes the code purely with CSS/width on the <svg>
// element. Dark modules are drawn as a single <path>; the light background is
// a full-bleed <rect>, so the result stays small.
//
// The error-correction level is Medium - the standard tradeoff between
// density and scan robustness for a screen-displayed join URL. The colors are
// fixed to black-on-white: a QR scanner needs high contrast, and the
// surrounding lobby card supplies the themed framing.
func SVG(data []byte) (string, error) {
	code, err := goqr.New(string(data), goqr.Medium)
	if err != nil {
		return "", fmt.Errorf("failed to encode QR code: %w", err)
	}

	// Bitmap() already includes the quiet zone; bitmap[y][x] is a dark module.
	return renderSVG(code.Bitmap()), nil
}

// renderSVG turns a square module bitmap (true = dark module, including the
// quiet-zone border) into a standalone SVG document. Each dark module is one
// 1x1 unit in the viewBox.
func renderSVG(bitmap [][]bool) string {
	dim := len(bitmap)

	var path strings.Builder
	for y := range dim {
		row := bitmap[y]
		for x := range row {
			if row[x] {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x, y)
			}
		}
	}

	var sb strings.Builder
	fmt.Fprintf(
		&sb,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="Join QR code">`,
		dim,
		dim,
	)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="#ffffff"/>`, dim, dim)
	fmt.Fprintf(&sb, `<path fill="#000000" d="%s"/>`, path.String())
	sb.WriteString("</svg>")

	return sb.String()
}
