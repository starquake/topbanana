package media_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"sync"
	"testing"

	. "github.com/starquake/topbanana/internal/media"
)

// gradient draws a deterministic w x h RGBA image. The same dimensions always
// produce the same pixels, so encoding it through a given format gives a stable
// input the sha256 determinism test can rely on.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 255) / max(w-1, 1)),
				G: uint8((y * 255) / max(h-1, 1)),
				B: 128,
				A: 255,
			})
		}
	}

	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode err = %v, want nil", err)
	}

	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode err = %v, want nil", err)
	}

	return buf.Bytes()
}

// TestProcessAcceptedFormats pins that each accepted input format (jpeg, png)
// decodes and processes into a valid jpeg full image and thumbnail.
func TestProcessAcceptedFormats(t *testing.T) {
	t.Parallel()

	img := gradient(200, 120)
	cases := map[string][]byte{
		"jpeg": encodeJPEG(t, img),
		"png":  encodePNG(t, img),
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Process(bytes.NewReader(raw), MaxUploadBytes)
			if err != nil {
				t.Fatalf("Process(%s) err = %v, want nil", name, err)
			}
			if got, want := got.MIME, "image/jpeg"; got != want {
				t.Errorf("MIME = %q, want %q", got, want)
			}
			if len(got.Full) == 0 {
				t.Error("Full is empty, want jpeg bytes")
			}
			if len(got.Thumb) == 0 {
				t.Error("Thumb is empty, want jpeg bytes")
			}
		})
	}
}

// TestProcessRoundTrip is the encoder correctness guard: the produced full and
// thumb bytes must decode back as valid jpeg images at the dimensions Process
// reports, so a misbehaving encoder is caught here rather than at serve time.
func TestProcessRoundTrip(t *testing.T) {
	t.Parallel()

	raw := encodePNG(t, gradient(800, 600))
	got, err := Process(bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process err = %v, want nil", err)
	}

	fullCfg, err := DecodeJPEGConfigForTest(bytes.NewReader(got.Full))
	if err != nil {
		t.Fatalf("DecodeConfig(Full) err = %v, want nil", err)
	}
	if fullCfg.Width != got.Width || fullCfg.Height != got.Height {
		t.Errorf("Full decoded = %dx%d, want %dx%d (reported)",
			fullCfg.Width, fullCfg.Height, got.Width, got.Height)
	}

	fullImg, err := DecodeJPEGForTest(bytes.NewReader(got.Full))
	if err != nil {
		t.Fatalf("Decode(Full) err = %v, want nil", err)
	}
	if got, want := fullImg.Bounds().Dx(), got.Width; got != want {
		t.Errorf("decoded Full width = %d, want %d", got, want)
	}

	thumbImg, err := DecodeJPEGForTest(bytes.NewReader(got.Thumb))
	if err != nil {
		t.Fatalf("Decode(Thumb) err = %v, want nil", err)
	}
	if longEdge(thumbImg.Bounds()) > ThumbLongEdge {
		t.Errorf("thumb long edge = %d, want <= %d", longEdge(thumbImg.Bounds()), ThumbLongEdge)
	}
}

// TestProcessDownscalesLongEdge pins that an oversized image is downscaled so
// its long edge caps at MaxLongEdge (full) and ThumbLongEdge (thumb), with the
// aspect ratio preserved.
func TestProcessDownscalesLongEdge(t *testing.T) {
	t.Parallel()

	// 3200x1600: long edge double MaxLongEdge, 2:1 aspect ratio.
	raw := encodePNG(t, gradient(3200, 1600))
	got, err := Process(bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process err = %v, want nil", err)
	}

	if got.Width != MaxLongEdge {
		t.Errorf("Width = %d, want %d (long edge capped)", got.Width, MaxLongEdge)
	}
	if got.Height != MaxLongEdge/2 {
		t.Errorf("Height = %d, want %d (2:1 aspect preserved)", got.Height, MaxLongEdge/2)
	}

	thumb, err := DecodeJPEGConfigForTest(bytes.NewReader(got.Thumb))
	if err != nil {
		t.Fatalf("DecodeConfig(Thumb) err = %v, want nil", err)
	}
	if thumb.Width != ThumbLongEdge {
		t.Errorf("thumb Width = %d, want %d", thumb.Width, ThumbLongEdge)
	}
	if thumb.Height != ThumbLongEdge/2 {
		t.Errorf("thumb Height = %d, want %d (2:1 aspect preserved)", thumb.Height, ThumbLongEdge/2)
	}
}

// TestProcessNeverUpscales pins that a small image passes through at its native
// size: the full image is never enlarged to MaxLongEdge.
func TestProcessNeverUpscales(t *testing.T) {
	t.Parallel()

	raw := encodePNG(t, gradient(100, 60))
	got, err := Process(bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process err = %v, want nil", err)
	}

	if got.Width != 100 || got.Height != 60 {
		t.Errorf("stored dims = %dx%d, want 100x60 (no upscale)", got.Width, got.Height)
	}
}

// TestProcessSHA256Deterministic pins that the same input bytes produce the
// same stored full image and thus the same sha256, which the cleanup tooling
// and the HTTP ETag depend on.
func TestProcessSHA256Deterministic(t *testing.T) {
	t.Parallel()

	raw := encodePNG(t, gradient(640, 480))

	first, err := Process(bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process #1 err = %v, want nil", err)
	}
	second, err := Process(bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process #2 err = %v, want nil", err)
	}

	if got, want := second.SHA256, first.SHA256; got != want {
		t.Errorf("sha256 = %q, want %q (deterministic)", got, want)
	}
	if !bytes.Equal(first.Full, second.Full) {
		t.Error("Full bytes differ between identical inputs, want identical")
	}
	if got, want := first.SizeBytes, len(first.Full); got != want {
		t.Errorf("SizeBytes = %d, want %d (len(Full))", got, want)
	}
}

// TestProcessRejectsOversize pins the raw-size guard: an upload past the cap is
// rejected before decode.
func TestProcessRejectsOversize(t *testing.T) {
	t.Parallel()

	oversize := bytes.Repeat([]byte{0xff}, MaxUploadBytes+1)
	_, err := Process(bytes.NewReader(oversize), MaxUploadBytes)
	if got, want := err, ErrUploadTooLarge; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// TestProcessHonorsCustomCap pins that the cap is the maxBytes argument, not a
// fixed constant: an image the default cap accepts is rejected under a tiny cap.
func TestProcessHonorsCustomCap(t *testing.T) {
	t.Parallel()

	raw := encodePNG(t, gradient(200, 120))
	if int64(len(raw)) > MaxUploadBytes {
		t.Fatalf("test input %d bytes exceeds the default cap %d", len(raw), MaxUploadBytes)
	}

	if _, err := Process(bytes.NewReader(raw), MaxUploadBytes); err != nil {
		t.Fatalf("Process under default cap err = %v, want nil", err)
	}

	tinyCap := int64(len(raw) - 1)
	_, err := Process(bytes.NewReader(raw), tinyCap)
	if got, want := err, ErrUploadTooLarge; !errors.Is(got, want) {
		t.Errorf("err under tiny cap = %v, want %v", got, want)
	}
}

// TestProcessRejectsEmpty pins that a zero-byte upload is rejected.
func TestProcessRejectsEmpty(t *testing.T) {
	t.Parallel()

	_, err := Process(bytes.NewReader(nil), MaxUploadBytes)
	if got, want := err, ErrEmptyUpload; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// TestProcessRejectsNonImage pins that undecodable bytes (and a non-accepted
// format) are rejected as ErrUnsupportedImage rather than panicking or
// producing garbage output.
func TestProcessRejectsNonImage(t *testing.T) {
	t.Parallel()

	_, err := Process(bytes.NewReader([]byte("this is plainly not an image at all")), MaxUploadBytes)
	if got, want := err, ErrUnsupportedImage; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// longEdge returns the larger of a rectangle's two dimensions.
func longEdge(r image.Rectangle) int {
	return max(r.Dx(), r.Dy())
}

// TestProcess_Concurrent runs many Process calls at once and pins that
// concurrent uploads stay race-free. The encoder is now stdlib image/jpeg
// which is safe for concurrent use (each call has its own buffer); this test
// stays in place to catch any future encoder swap that breaks that property.
func TestProcess_Concurrent(t *testing.T) {
	t.Parallel()

	inputPNG := encodePNG(t, gradient(200, 150))

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if _, err := Process(bytes.NewReader(inputPNG), MaxUploadBytes); err != nil {
				t.Errorf("Process err = %v, want nil", err)
			}
		}()
	}
	wg.Wait()
}

// pngHeader builds a valid PNG signature + IHDR chunk declaring w x h, with no
// pixel data. image.DecodeConfig reads only the header, so this is enough to
// report the declared dimensions - the basis of a decode bomb: a tiny file that
// claims an enormous size.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // colour type: truecolour

	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.WriteString("IHDR")
	b.Write(ihdr)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte("IHDR"))
	_, _ = crc.Write(ihdr)
	_ = binary.Write(&b, binary.BigEndian, crc.Sum32())

	return b.Bytes()
}

// TestProcess_RejectsDecodeBomb pins the decode-bomb guard: a small upload that
// declares a huge pixel area is rejected from its header (DecodeConfig) before
// Process attempts the full decode that would allocate gigabytes.
func TestProcess_RejectsDecodeBomb(t *testing.T) {
	t.Parallel()

	// 60000 x 60000 = 3.6e9 px, far over MaxPixels (5e7); the file itself is a
	// few dozen bytes.
	bomb := pngHeader(60000, 60000)
	if got := len(bomb); got > 1024 {
		t.Fatalf("bomb header = %d bytes, want a tiny file", got)
	}

	_, err := Process(bytes.NewReader(bomb), MaxUploadBytes)
	if got, want := err, ErrImageTooLarge; !errors.Is(got, want) {
		t.Errorf("Process(decode bomb) err = %v, want %v", got, want)
	}
}
