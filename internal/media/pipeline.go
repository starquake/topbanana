// Package media processes uploaded images into normalised jpeg bytes plus
// metadata, and ties that pipeline to filesystem and database persistence.
//
// The pipeline is pure (no disk, no DB): it takes the raw upload bytes and
// returns the stored full-image jpeg, a thumbnail jpeg, and the metadata a
// media row records. The persistence half (Service) writes those bytes under
// a per-quiz directory and inserts the row.
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // register the png decoder with image.Decode
	"io"
	"math"

	"golang.org/x/image/draw"
)

const (
	// MaxUploadBytes is the documented default cap on the raw upload size
	// (~10 MB). The runtime cap is configurable (MEDIA_IMAGE_MAX_BYTES, threaded
	// through Service into Process); this constant remains the default value and
	// the basis for the multipart request envelope in internal/mediahttp. A
	// larger upload is rejected before decode so a hostile or accidental huge
	// file cannot make the decoder allocate unbounded memory.
	MaxUploadBytes = 10 << 20

	// MaxPixels caps the decoded pixel area (~50 megapixels). The byte cap does
	// not bound the decoded allocation: a tiny file can declare enormous
	// dimensions (a "decode bomb") that forces a multi-gigabyte RGBA buffer. The
	// declared size is checked via DecodeConfig (header only) before the full
	// decode. 50 MP is far above any real photo yet rejects the bombs, and the
	// output is only MaxLongEdge px so a larger source is never needed.
	MaxPixels = 50_000_000

	// MaxDecodedBytes caps the decoded pixel buffer estimated from the header's
	// colour model: a 16-bit png decodes at 8 bytes per pixel, so MaxPixels
	// alone still allows a ~400 MB buffer from a small file.
	MaxDecodedBytes = 200 << 20

	// maxConcurrentDecodes bounds how many uploads decode at once, so parallel
	// uploads cannot multiply the per-image peak memory.
	maxConcurrentDecodes = 2

	grayPixelBytes   = 1
	gray16PixelBytes = 2
	ycbcrPixelBytes  = 3
	rgbaPixelBytes   = 4
	rgba64PixelBytes = 8

	// MaxLongEdge caps the stored full image's long edge in pixels. The image
	// is only ever downscaled to this; a smaller image passes through at its
	// native size (never upscaled). Sized for the admin lightbox at typical
	// laptop / desktop resolutions; a higher cap inflated stored bytes
	// without a visible quality win at that scale.
	MaxLongEdge = 1200

	// ThumbLongEdge caps the pre-generated thumbnail's long edge in pixels.
	// Sized for a retina library grid.
	ThumbLongEdge = 480

	// jpegQuality is the lossy jpeg encode quality for both the full image
	// and the thumbnail.
	jpegQuality = 75

	// MIMEJPEG is the mime type every stored image carries; the pipeline
	// normalises every accepted input (jpeg or png) to jpeg.
	MIMEJPEG = "image/jpeg"
)

// ErrUploadTooLarge is returned when the raw upload exceeds the configured cap.
var ErrUploadTooLarge = errors.New("upload exceeds maximum size")

// ErrEmptyUpload is returned when the upload contains no bytes.
var ErrEmptyUpload = errors.New("upload is empty")

// ErrUnsupportedImage is returned when the upload cannot be decoded as one of
// the accepted formats (jpeg or png).
var ErrUnsupportedImage = errors.New("unsupported or undecodable image")

// ErrImageTooLarge is returned when the decoded image's pixel dimensions exceed
// MaxPixels or its estimated decoded size exceeds MaxDecodedBytes - a
// decode-bomb guard checked from the header before the full decode allocates
// the pixel buffer.
var ErrImageTooLarge = errors.New("image dimensions exceed maximum")

//nolint:gochecknoglobals // process-wide decode semaphore.
var decodeSlots = make(chan struct{}, maxConcurrentDecodes)

// Processed is the output of the pipeline: the normalised full-image and
// thumbnail jpeg bytes plus the metadata a media row stores. Width, Height,
// SizeBytes, and SHA256 describe the stored full image (Full), not the thumb.
type Processed struct {
	// Full is the resized, jpeg-encoded stored image.
	Full []byte
	// Thumb is the smaller jpeg thumbnail derived from the same source.
	Thumb []byte
	// Width and Height are the dimensions of the stored full image in pixels.
	Width  int
	Height int
	// SizeBytes is len(Full): the stored full image's byte length.
	SizeBytes int
	// SHA256 is the lowercase-hex sha256 of Full, used for integrity checks
	// and as the HTTP ETag when the image is served.
	SHA256 string
	// MIME is always MIMEJPEG.
	MIME string
}

// Process decodes the upload (jpeg or png), downscales it so its long edge is
// at most MaxLongEdge, re-encodes it as lossy jpeg, and derives a
// ThumbLongEdge jpeg thumbnail from the same decoded source. A jpeg's EXIF
// Orientation is applied to both outputs. It is pure: no disk or network. The
// reader is fully consumed. maxBytes caps the raw upload size; zero or negative
// disables the cap.
//
// At most maxConcurrentDecodes calls decode at once; a call waiting for a slot
// returns ctx's error when ctx ends first.
//
// Returns ErrUploadTooLarge when the raw bytes exceed maxBytes, ErrEmptyUpload
// for a zero-byte upload, and ErrUnsupportedImage when the bytes are not a
// decodable jpeg or png.
func Process(ctx context.Context, r io.Reader, maxBytes int64) (*Processed, error) {
	raw, err := readCapped(r, maxBytes)
	if err != nil {
		return nil, err
	}

	release, err := acquireDecodeSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	src, format, err := decodeGuarded(raw)
	if err != nil {
		return nil, err
	}

	orientation := orientationNormal
	if format == "jpeg" {
		orientation = jpegOrientation(raw)
	}
	full := applyOrientation(resizeLongEdge(src, MaxLongEdge), orientation)
	thumb := applyOrientation(resizeLongEdge(src, ThumbLongEdge), orientation)

	fullBytes, err := encodeJPEG(full)
	if err != nil {
		return nil, err
	}
	thumbBytes, err := encodeJPEG(thumb)
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
		MIME:      MIMEJPEG,
	}, nil
}

// acquireDecodeSlot blocks until a decode slot is free or ctx ends, and
// returns the func that frees the slot.
func acquireDecodeSlot(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("waiting for decode slot: %w", err)
	}
	select {
	case decodeSlots <- struct{}{}:
		return func() { <-decodeSlots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for decode slot: %w", ctx.Err())
	}
}

// decodeGuarded decodes raw (jpeg or png) and returns the image and its format
// name. It rejects a decode bomb from the header first (DecodeConfig reads only
// the header; PNG's max declared edge of 2^31 keeps the int64 area product in
// range, so it cannot overflow), then decodes the full image. Returns
// ErrImageTooLarge for an oversized declared area or decoded size and
// ErrUnsupportedImage for undecodable bytes.
func decodeGuarded(raw []byte) (image.Image, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrUnsupportedImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", ErrImageTooLarge
	}
	area := int64(cfg.Width) * int64(cfg.Height)
	if area > MaxPixels || area*bytesPerPixel(cfg.ColorModel) > MaxDecodedBytes {
		return nil, "", ErrImageTooLarge
	}

	src, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrUnsupportedImage, err)
	}

	return src, format, nil
}

// bytesPerPixel is the decoded buffer cost per pixel of the image type the
// stdlib decoders return for m; an unknown model assumes the 16-bit worst case.
func bytesPerPixel(m color.Model) int64 {
	// A Palette is a slice, so it must be matched before the == comparisons.
	if _, ok := m.(color.Palette); ok {
		return grayPixelBytes
	}
	switch m {
	case color.GrayModel, color.AlphaModel:
		return grayPixelBytes
	case color.Gray16Model, color.Alpha16Model:
		return gray16PixelBytes
	case color.YCbCrModel:
		return ycbcrPixelBytes
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel, color.NYCbCrAModel:
		return rgbaPixelBytes
	default:
		return rgba64PixelBytes
	}
}

// readCapped reads the upload, capping it at maxBytes (plus one byte to detect
// an over-cap upload). A zero or negative cap disables the limit. An empty
// upload is rejected.
func readCapped(r io.Reader, maxBytes int64) ([]byte, error) {
	reader := r
	if maxBytes > 0 {
		// Saturate the read limit rather than computing maxBytes+1: a cap at
		// math.MaxInt64 would overflow to a negative limit, which LimitReader
		// treats as "read nothing" and would fail every upload as empty. At the
		// saturated limit the over-cap check below is unreachable, but a cap that
		// high is effectively no cap.
		limit := int64(math.MaxInt64)
		if maxBytes < math.MaxInt64 {
			limit = maxBytes + 1
		}
		reader = io.LimitReader(r, limit)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading upload: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptyUpload
	}
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
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

// encodeJPEG encodes img as lossy jpeg at jpegQuality.
func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encoding jpeg: %w", err)
	}

	return buf.Bytes(), nil
}
