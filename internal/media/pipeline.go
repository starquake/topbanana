// Package media processes uploaded images into normalised webp bytes plus
// metadata, and ties that pipeline to filesystem and database persistence.
//
// The pipeline is pure (no disk, no DB): it takes the raw upload bytes and
// returns the stored full-image webp, a thumbnail webp, and the metadata a
// media row records. The persistence half (Service) writes those bytes under
// a per-quiz directory and inserts the row.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register the jpeg decoder with image.Decode
	_ "image/png"  // register the png decoder with image.Decode
	"io"

	"github.com/deepteams/webp"
	"golang.org/x/image/draw"
)

const (
	// MaxUploadBytes caps the raw upload size (~10 MB). A larger upload is
	// rejected before decode so a hostile or accidental huge file cannot make
	// the decoder allocate unbounded memory.
	MaxUploadBytes = 10 << 20

	// MaxPixels caps the decoded pixel area (~50 megapixels). The byte cap does
	// not bound the decoded allocation: a tiny file can declare enormous
	// dimensions (a "decode bomb") that forces a multi-gigabyte RGBA buffer. The
	// declared size is checked via DecodeConfig (header only) before the full
	// decode. 50 MP is far above any real photo yet rejects the bombs, and the
	// output is only MaxLongEdge px so a larger source is never needed.
	MaxPixels = 50_000_000

	// MaxLongEdge caps the stored full image's long edge in pixels. The image
	// is only ever downscaled to this; a smaller image passes through at its
	// native size (never upscaled).
	MaxLongEdge = 1600

	// ThumbLongEdge caps the pre-generated thumbnail's long edge in pixels.
	// Sized for a retina library grid.
	ThumbLongEdge = 480

	// webpQuality is the lossy webp encode quality for both the full image and
	// the thumbnail.
	webpQuality = 80

	// MIMEWebP is the mime type every stored image carries; the pipeline
	// always normalises to webp regardless of the input format.
	MIMEWebP = "image/webp"
)

// ErrUploadTooLarge is returned when the raw upload exceeds MaxUploadBytes.
var ErrUploadTooLarge = errors.New("upload exceeds maximum size")

// ErrEmptyUpload is returned when the upload contains no bytes.
var ErrEmptyUpload = errors.New("upload is empty")

// ErrUnsupportedImage is returned when the upload cannot be decoded as one of
// the accepted formats (jpeg, png, webp).
var ErrUnsupportedImage = errors.New("unsupported or undecodable image")

// ErrImageTooLarge is returned when the decoded image's pixel dimensions exceed
// MaxPixels - a decode-bomb guard checked from the header before the full
// decode allocates the pixel buffer.
var ErrImageTooLarge = errors.New("image dimensions exceed maximum")

// Processed is the output of the pipeline: the normalised full-image and
// thumbnail webp bytes plus the metadata a media row stores. Width, Height,
// SizeBytes, and SHA256 describe the stored full image (Full), not the thumb.
type Processed struct {
	// Full is the resized, webp-encoded stored image.
	Full []byte
	// Thumb is the smaller webp thumbnail derived from the same source.
	Thumb []byte
	// Width and Height are the dimensions of the stored full image in pixels.
	Width  int
	Height int
	// SizeBytes is len(Full): the stored full image's byte length.
	SizeBytes int
	// SHA256 is the lowercase-hex sha256 of Full, used for integrity checks
	// and as the HTTP ETag when the image is served.
	SHA256 string
	// MIME is always MIMEWebP.
	MIME string
}

// Process decodes the upload (jpeg, png, or webp), downscales it so its long
// edge is at most MaxLongEdge, re-encodes it as lossy webp, and derives a
// ThumbLongEdge webp thumbnail from the same decoded source. It is pure: no
// disk or network. The reader is fully consumed.
//
// Returns ErrUploadTooLarge when the raw bytes exceed MaxUploadBytes,
// ErrEmptyUpload for a zero-byte upload, and ErrUnsupportedImage when the
// bytes are not a decodable jpeg/png/webp.
func Process(r io.Reader) (*Processed, error) {
	raw, err := readCapped(r)
	if err != nil {
		return nil, err
	}

	// Reject a decode bomb before the full decode allocates the pixel buffer:
	// DecodeConfig reads only the header, so checking the declared dimensions
	// costs nothing. PNG's max declared edge (2^31) keeps the int64 product in
	// range, so the area multiply cannot overflow.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, ErrImageTooLarge
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedImage, err)
	}

	full := resizeLongEdge(src, MaxLongEdge)
	thumb := resizeLongEdge(src, ThumbLongEdge)

	fullBytes, err := encodeWebP(full)
	if err != nil {
		return nil, err
	}
	thumbBytes, err := encodeWebP(thumb)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(fullBytes)
	bounds := full.Bounds()

	return &Processed{
		Full:      fullBytes,
		Thumb:     thumbBytes,
		Width:     bounds.Dx(),
		Height:    bounds.Dy(),
		SizeBytes: len(fullBytes),
		SHA256:    hex.EncodeToString(sum[:]),
		MIME:      MIMEWebP,
	}, nil
}

// readCapped reads at most MaxUploadBytes+1 from r so the result both holds the
// whole upload (when within the cap) and detects an over-cap upload by the
// extra byte. An empty upload is rejected.
func readCapped(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading upload: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptyUpload
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}

	return raw, nil
}

// resizeLongEdge returns src scaled so its long edge is at most maxLongEdge,
// preserving aspect ratio. An image already within the cap is returned
// unchanged (never upscaled). draw.CatmullRom gives a high-quality downscale.
func resizeLongEdge(src image.Image, maxLongEdge int) image.Image {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	longEdge := max(h, w)
	if longEdge <= maxLongEdge {
		return src
	}

	scale := float64(maxLongEdge) / float64(longEdge)
	dw := max(int(float64(w)*scale), 1)
	dh := max(int(float64(h)*scale), 1)

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	return dst
}

// encodeWebP encodes img as lossy webp at webpQuality.
func encodeWebP(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, &webp.EncoderOptions{Quality: webpQuality}); err != nil {
		return nil, fmt.Errorf("encoding webp: %w", err)
	}

	return buf.Bytes(), nil
}
