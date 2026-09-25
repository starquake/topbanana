package media_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"strconv"
	"testing"

	. "github.com/starquake/topbanana/internal/media"
)

// tiffEntry is one IFD0 entry: tag, type, count, and the 4-byte value field.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	value    uint16
}

// exifTIFF builds a TIFF block holding the given IFD0 entries.
func exifTIFF(order binary.ByteOrder, entries ...tiffEntry) []byte {
	var b bytes.Buffer
	if order == binary.ByteOrder(binary.LittleEndian) {
		b.WriteString("II")
	} else {
		b.WriteString("MM")
	}
	_ = binary.Write(&b, order, uint16(42))
	_ = binary.Write(&b, order, uint32(8))
	_ = binary.Write(&b, order, uint16(len(entries)))
	for _, e := range entries {
		_ = binary.Write(&b, order, e.tag)
		_ = binary.Write(&b, order, e.typ)
		_ = binary.Write(&b, order, e.count)
		_ = binary.Write(&b, order, e.value)
		_ = binary.Write(&b, order, uint16(0))
	}
	_ = binary.Write(&b, order, uint32(0))

	return b.Bytes()
}

// orientationEntry is a well-formed Orientation entry with value v.
func orientationEntry(v uint16) tiffEntry {
	return tiffEntry{tag: 0x0112, typ: 3, count: 1, value: v}
}

// withSegment inserts a marker segment carrying payload right after the SOI of
// a jpeg.
func withSegment(jpg []byte, marker byte, payload []byte) []byte {
	var b bytes.Buffer
	b.Write(jpg[:2])
	b.Write([]byte{0xFF, marker})
	_ = binary.Write(&b, binary.BigEndian, uint16(len(payload)+2))
	b.Write(payload)
	b.Write(jpg[2:])

	return b.Bytes()
}

// withEXIF inserts an APP1 EXIF segment holding tiff into a jpeg.
func withEXIF(jpg, tiff []byte) []byte {
	return withSegment(jpg, 0xE1, append([]byte("Exif\x00\x00"), tiff...))
}

func TestJPEGOrientation(t *testing.T) {
	t.Parallel()

	plain := encodeJPEG(t, gradient(8, 8))
	le, be := binary.LittleEndian, binary.BigEndian
	cases := map[string]struct {
		raw  []byte
		want int
	}{
		"no exif":       {plain, 1},
		"not a jpeg":    {[]byte("not a jpeg"), 1},
		"little endian": {withEXIF(plain, exifTIFF(le, orientationEntry(6))), 6},
		"big endian":    {withEXIF(plain, exifTIFF(be, orientationEntry(8))), 8},
		"after app0": {
			withSegment(withEXIF(plain, exifTIFF(le, orientationEntry(3))), 0xE0, []byte("JFIF\x00")),
			3,
		},
		"not first tag": {
			withEXIF(plain, exifTIFF(be, tiffEntry{tag: 0x010F, typ: 2, count: 1}, orientationEntry(5))),
			5,
		},
		"out of range":   {withEXIF(plain, exifTIFF(le, orientationEntry(9))), 1},
		"zero":           {withEXIF(plain, exifTIFF(le, orientationEntry(0))), 1},
		"wrong type":     {withEXIF(plain, exifTIFF(le, tiffEntry{tag: 0x0112, typ: 4, count: 1, value: 6})), 1},
		"bad byte order": {withEXIF(plain, []byte("XX\x00\x2a\x08\x00\x00\x00")), 1},
		"truncated ifd":  {withEXIF(plain, exifTIFF(le, orientationEntry(6))[:12]), 1},
		"ifd offset out": {withEXIF(plain, []byte("II\x2a\x00\xff\xff\xff\xff")), 1},
		"truncated file": {withEXIF(plain, exifTIFF(le, orientationEntry(6)))[:20], 1},
		"no tiff header": {withEXIF(plain, nil), 1},
		"other app1":     {withSegment(plain, 0xE1, []byte("http://ns.adobe.com/xap/1.0/\x00")), 1},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, want := ExportJPEGOrientation(tc.raw), tc.want; got != want {
				t.Errorf("jpegOrientation = %d, want %d", got, want)
			}
		})
	}
}

// markedImage is a w x h white image with a red 16 x 16 block in its top-left
// corner, so the block's corner after processing identifies the transform.
func markedImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if x < 16 && y < 16 {
				c = color.RGBA{R: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}

	return img
}

// redCorner reports which corner of img holds the red marker block.
func redCorner(img image.Image) string {
	b := img.Bounds()
	corners := map[string]image.Point{
		"top-left":     {b.Min.X + 6, b.Min.Y + 6},
		"top-right":    {b.Max.X - 7, b.Min.Y + 6},
		"bottom-left":  {b.Min.X + 6, b.Max.Y - 7},
		"bottom-right": {b.Max.X - 7, b.Max.Y - 7},
	}
	found := ""
	for name, p := range corners {
		r, g, _, _ := img.At(p.X, p.Y).RGBA()
		if r>>8 > 200 && g>>8 < 80 {
			if found != "" {
				return "several"
			}
			found = name
		}
	}

	return found
}

// TestProcess_AppliesEXIFOrientation pins all eight orientation values, in both
// TIFF byte orders, on the full image and the thumbnail.
func TestProcess_AppliesEXIFOrientation(t *testing.T) {
	t.Parallel()

	const w, h = 64, 32
	src := encodeJPEG(t, markedImage(w, h))
	cases := []struct {
		orientation   uint16
		wantW, wantH  int
		wantRedCorner string
	}{
		{1, w, h, "top-left"},
		{2, w, h, "top-right"},
		{3, w, h, "bottom-right"},
		{4, w, h, "bottom-left"},
		{5, h, w, "top-left"},
		{6, h, w, "top-right"},
		{7, h, w, "bottom-right"},
		{8, h, w, "bottom-left"},
	}
	orders := map[string]binary.ByteOrder{"II": binary.LittleEndian, "MM": binary.BigEndian}

	for orderName, order := range orders {
		for _, tc := range cases {
			t.Run(orderName+"/"+strconv.Itoa(int(tc.orientation)), func(t *testing.T) {
				t.Parallel()

				raw := withEXIF(src, exifTIFF(order, orientationEntry(tc.orientation)))
				got, err := Process(t.Context(), bytes.NewReader(raw), MaxUploadBytes)
				if err != nil {
					t.Fatalf("Process err = %v, want nil", err)
				}
				if got, want := got.Width, tc.wantW; got != want {
					t.Errorf("Width = %d, want %d", got, want)
				}
				if got, want := got.Height, tc.wantH; got != want {
					t.Errorf("Height = %d, want %d", got, want)
				}
				for name, data := range map[string][]byte{"full": got.Full, "thumb": got.Thumb} {
					img, err := DecodeJPEGForTest(bytes.NewReader(data))
					if err != nil {
						t.Fatalf("decode %s err = %v, want nil", name, err)
					}
					if got, want := img.Bounds().Size(), image.Pt(tc.wantW, tc.wantH); got != want {
						t.Errorf("%s size = %v, want %v", name, got, want)
					}
					if got, want := redCorner(img), tc.wantRedCorner; got != want {
						t.Errorf("%s red corner = %q, want %q", name, got, want)
					}
				}
			})
		}
	}
}

// TestProcess_OrientationAfterDownscale pins that a rotated image larger than
// MaxLongEdge is both downscaled and rotated.
func TestProcess_OrientationAfterDownscale(t *testing.T) {
	t.Parallel()

	raw := withEXIF(encodeJPEG(t, markedImage(2400, 1200)),
		exifTIFF(binary.BigEndian, orientationEntry(6)))
	got, err := Process(t.Context(), bytes.NewReader(raw), MaxUploadBytes)
	if err != nil {
		t.Fatalf("Process err = %v, want nil", err)
	}
	if got, want := image.Pt(got.Width, got.Height), image.Pt(MaxLongEdge/2, MaxLongEdge); got != want {
		t.Errorf("stored size = %v, want %v", got, want)
	}
	img, err := DecodeJPEGForTest(bytes.NewReader(got.Full))
	if err != nil {
		t.Fatalf("decode err = %v, want nil", err)
	}
	if got, want := redCorner(img), "top-right"; got != want {
		t.Errorf("red corner = %q, want %q", got, want)
	}
}
