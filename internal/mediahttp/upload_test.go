package mediahttp_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/mediahttp"
)

func TestWantsJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		accept string
		want   bool
	}{
		{name: "empty header", accept: "", want: false},
		{name: "application/json exact", accept: "application/json", want: true},
		{name: "application/json with charset", accept: "application/json; charset=utf-8", want: true},
		{name: "first of two", accept: "application/json, text/html", want: true},
		{name: "second of two", accept: "text/html, application/json", want: true},
		{name: "case-insensitive", accept: "Application/JSON", want: true},
		{name: "plain html", accept: "text/html", want: false},
		{name: "html with q-weight", accept: "text/html;q=0.9, application/xhtml+xml", want: false},
		{name: "wildcard does not count", accept: "*/*", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			if got, want := mediahttp.WantsJSON(req), tc.want; got != want {
				t.Errorf("WantsJSON(%q) = %v, want %v", tc.accept, got, want)
			}
		})
	}
}

func TestUploadFailureReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "upload too large", err: media.ErrUploadTooLarge, want: "file exceeds the maximum upload size"},
		{name: "image too large", err: media.ErrImageTooLarge, want: "image dimensions exceed the maximum"},
		{name: "empty upload", err: media.ErrEmptyUpload, want: "file is empty"},
		{
			name: "unsupported image",
			err:  media.ErrUnsupportedImage,
			want: "unsupported image format (use jpg, png, or webp)",
		},
		{name: "unknown error", err: errors.New("some random failure"), want: "upload failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := mediahttp.UploadFailureReason(tc.err), tc.want; got != want {
				t.Errorf("UploadFailureReason(%v) = %q, want %q", tc.err, got, want)
			}
		})
	}
}

func TestBuildUploadQuery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		uploaded int
		failed   int
		want     string
	}{
		{name: "nothing happened", uploaded: 0, failed: 0, want: ""},
		{name: "single success", uploaded: 1, failed: 0, want: "?uploaded=1&failed=0"},
		{name: "single failure", uploaded: 0, failed: 1, want: "?uploaded=0&failed=1"},
		{name: "mixed", uploaded: 3, failed: 2, want: "?uploaded=3&failed=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := mediahttp.BuildUploadQuery(tc.uploaded, tc.failed), tc.want; got != want {
				t.Errorf("BuildUploadQuery(%d, %d) = %q, want %q",
					tc.uploaded, tc.failed, got, want)
			}
		})
	}
}

func TestWriteUploadJSON(t *testing.T) {
	t.Parallel()

	t.Run("non-pipeline error returns 500", func(t *testing.T) {
		t.Parallel()
		results := []mediahttp.UploadResult{
			{Filename: "good.png", MediaID: 42},
			{Filename: "broken.png", Err: errors.New("disk full")},
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", nil)
		mediahttp.WriteUploadJSON(rec, req, slog.New(slog.DiscardHandler), results)
		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("writeUploadJSON(non-pipeline error) status = %d, want %d", got, want)
		}
		if got, want := rec.Body.String(), "internal error"; !strings.Contains(got, want) {
			t.Errorf("writeUploadJSON(non-pipeline error) body = %q, should contain %q", got, want)
		}
	})

	t.Run("pipeline only returns 200 json", func(t *testing.T) {
		t.Parallel()
		results := []mediahttp.UploadResult{
			{Filename: "good.png", MediaID: 7},
			{Filename: "bad.txt", Err: media.ErrUnsupportedImage},
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", nil)
		mediahttp.WriteUploadJSON(rec, req, slog.New(slog.DiscardHandler), results)
		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("writeUploadJSON(pipeline only) status = %d, want %d", got, want)
		}
		if got, want := rec.Header().Get("Content-Type"), "application/json"; !strings.HasPrefix(got, want) {
			t.Errorf("writeUploadJSON(pipeline only) Content-Type = %q, want prefix %q", got, want)
		}
		type uploadedItem struct {
			Filename string `json:"filename"`
			ID       int64  `json:"id"`
		}
		type failedItem struct {
			Filename string `json:"filename"`
			Reason   string `json:"reason"`
		}
		var payload struct {
			Uploaded []uploadedItem `json:"uploaded"`
			Failed   []failedItem   `json:"failed"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("writeUploadJSON(pipeline only) Unmarshal err = %v, want nil", err)
		}
		if got, want := len(payload.Uploaded), 1; got != want {
			t.Fatalf("writeUploadJSON(pipeline only) uploaded len = %d, want %d", got, want)
		}
		if got, want := payload.Uploaded[0].ID, int64(7); got != want {
			t.Errorf("writeUploadJSON(pipeline only) uploaded[0].ID = %d, want %d", got, want)
		}
		if got, want := len(payload.Failed), 1; got != want {
			t.Fatalf("writeUploadJSON(pipeline only) failed len = %d, want %d", got, want)
		}
		if got, want := payload.Failed[0].Reason, "unsupported image"; !strings.Contains(got, want) {
			t.Errorf("writeUploadJSON(pipeline only) failed[0].Reason = %q, should contain %q", got, want)
		}
	})
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("first")
	other := errors.New("second")
	cases := []struct {
		name         string
		results      []mediahttp.UploadResult
		wantUploaded int
		wantFailed   int
		wantErrIs    error
	}{
		{name: "empty", results: nil, wantUploaded: 0, wantFailed: 0, wantErrIs: nil},
		{
			name: "all success",
			results: []mediahttp.UploadResult{
				{Filename: "a.png", MediaID: 1},
				{Filename: "b.png", MediaID: 2},
			},
			wantUploaded: 2,
			wantFailed:   0,
			wantErrIs:    nil,
		},
		{
			name: "all failed - first err returned",
			results: []mediahttp.UploadResult{
				{Filename: "a.png", Err: sentinel},
				{Filename: "b.png", Err: other},
			},
			wantUploaded: 0,
			wantFailed:   2,
			wantErrIs:    sentinel,
		},
		{
			name: "mixed - first failure is the one returned",
			results: []mediahttp.UploadResult{
				{Filename: "a.png", MediaID: 1},
				{Filename: "b.png", Err: sentinel},
				{Filename: "c.png", MediaID: 2},
				{Filename: "d.png", Err: other},
			},
			wantUploaded: 2,
			wantFailed:   2,
			wantErrIs:    sentinel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotUploaded, gotFailed, gotErr := mediahttp.Summarize(tc.results)
			if got, want := gotUploaded, tc.wantUploaded; got != want {
				t.Errorf("uploaded = %d, want %d", got, want)
			}
			if got, want := gotFailed, tc.wantFailed; got != want {
				t.Errorf("failed = %d, want %d", got, want)
			}
			if tc.wantErrIs == nil {
				if gotErr != nil {
					t.Errorf("firstErr = %v, want nil", gotErr)
				}
			} else if !errors.Is(gotErr, tc.wantErrIs) {
				t.Errorf("firstErr = %v, want %v", gotErr, tc.wantErrIs)
			}
		})
	}
}
