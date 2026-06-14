package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/store"
)

// TestMediaUpload_Integration covers the host/admin upload endpoint (#936
// slice 2): an owner can upload an image to their editable quiz and the image
// then serves back; a non-owner host is refused; and a malformed upload is
// rejected with 400.
func TestMediaUpload_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "media-boss@example.test",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "media-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "media-owner")
	other := registerAdminClient(ctx, t, baseURL, setup.DBURI, "media-other")
	makeHost(ctx, t, setup.DBURI, "media-owner")
	makeHost(ctx, t, setup.DBURI, "media-other")

	quizID := createQuizAs(ctx, t, owner, baseURL, "Media Owner Quiz")

	t.Run("owner uploads then serves back", func(t *testing.T) {
		t.Parallel()
		uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
		mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

		resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d", mediaID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("serve status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "image/webp"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		if etag := resp.Header.Get("ETag"); etag == "" {
			t.Error("ETag header is empty, want the stored sha256")
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body err = %v, want nil", err)
		}
		if len(body) == 0 {
			t.Error("served image body is empty")
		}
	})

	t.Run("non-owner host is refused", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, other, baseURL+"/admin/quizzes")
		body, contentType := multipartImage(t, "pic.png", pngBytes(t, 64, 64), token)
		req := newMultipartReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media", quizID), body, contentType)
		resp, err := other.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("non-owner upload status = %d, want %d", got, want)
		}
	})

	t.Run("non-image upload is 400", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, owner, baseURL+"/admin/quizzes")
		body, contentType := multipartImage(t, "note.txt", []byte("not an image at all"), token)
		req := newMultipartReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media", quizID), body, contentType)
		resp, err := owner.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
			t.Errorf("non-image upload status = %d, want %d", got, want)
		}
	})

	t.Run("missing file part is 400", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, owner, baseURL+"/admin/quizzes")
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if err := mw.WriteField("csrf_token", token); err != nil {
			t.Fatalf("WriteField err = %v, want nil", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("multipart Close err = %v, want nil", err)
		}
		req := newMultipartReq(
			ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media", quizID), &buf, mw.FormDataContentType(),
		)
		resp, err := owner.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
			t.Errorf("missing-file upload status = %d, want %d", got, want)
		}
	})
}

// TestMediaServe_Integration covers the serving endpoints (#936 slice 2):
// conditional requests, the thumbnail variant, the unknown-id 404, a garbage
// id, and the public-vs-private visibility gate.
func TestMediaServe_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "serve-boss@example.test",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "serve-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "serve-owner")
	makeHost(ctx, t, setup.DBURI, "serve-owner")

	publicQuiz := createQuizAs(ctx, t, owner, baseURL, "Public Serve Quiz")
	privateQuiz := createQuizWithVisibility(ctx, t, owner, baseURL, "Private Serve Quiz", "private")

	uploadImage(ctx, t, owner, baseURL, publicQuiz, "p.png", pngBytes(t, 240, 160))
	publicMedia := latestMediaID(ctx, t, setup.Stores, publicQuiz)
	uploadImage(ctx, t, owner, baseURL, privateQuiz, "s.png", pngBytes(t, 240, 160))
	privateMedia := latestMediaID(ctx, t, setup.Stores, privateQuiz)

	t.Run("public image then conditional 304", func(t *testing.T) {
		t.Parallel()
		etag := func() string {
			resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d", publicMedia))
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("public serve status = %d, want %d", got, want)
			}
			if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "public") {
				t.Errorf("public Cache-Control = %q, want it to contain %q", got, "public")
			}

			return resp.Header.Get("ETag")
		}()
		if etag == "" {
			t.Fatal("public serve ETag is empty")
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+fmt.Sprintf("/media/%d", publicMedia), nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.Header.Set("If-None-Match", etag)
		condResp, err := owner.Do(req)
		if err != nil {
			t.Fatalf("conditional Do err = %v, want nil", err)
		}
		defer closeBody(t, condResp.Body)
		if got, want := condResp.StatusCode, http.StatusNotModified; got != want {
			t.Errorf("conditional serve status = %d, want %d", got, want)
		}
	})

	t.Run("public image to anonymous viewer mints no session cookie", func(t *testing.T) {
		t.Parallel()
		anon := newAnonClient(t)
		resp := httpGet(ctx, t, anon, baseURL+fmt.Sprintf("/media/%d", publicMedia))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("anonymous public serve status = %d, want %d", got, want)
		}
		// Serving a cacheable image must not mint a player row or set a session
		// cookie (#936): a Set-Cookie on a Cache-Control: public response is a
		// shared-cache footgun that can leak one visitor's session to others.
		if got := resp.Header.Get("Set-Cookie"); got != "" {
			t.Errorf("public serve set cookie %q, want none (no player mint on a cacheable response)", got)
		}
	})

	t.Run("thumbnail serves", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d/thumb", publicMedia))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("thumb status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "image/webp"; got != want {
			t.Errorf("thumb Content-Type = %q, want %q", got, want)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, owner, baseURL+"/media/999999")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("unknown-id status = %d, want %d", got, want)
		}
	})

	t.Run("garbage id is 4xx", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, owner, baseURL+"/media/not-a-number")
		defer closeBody(t, resp.Body)
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("garbage-id status = %d, want a 4xx", resp.StatusCode)
		}
	})

	t.Run("private image refused to anonymous viewer", func(t *testing.T) {
		t.Parallel()
		anon := newAnonClient(t)
		resp := httpGet(ctx, t, anon, baseURL+fmt.Sprintf("/media/%d", privateMedia))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("anonymous private serve status = %d, want %d", got, want)
		}
	})

	t.Run("private image allowed to owner", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d", privateMedia))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("owner private serve status = %d, want %d", got, want)
		}
		if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "private") {
			t.Errorf("private Cache-Control = %q, want it to contain %q", got, "private")
		}
	})
}

// setupMedia boots an integration server with a per-test MEDIA_DIR (a t.TempDir
// the framework cleans up) and a store.Stores for resolving uploaded media ids
// directly, rather than the default ./media under the package working dir.
func setupMedia(t *testing.T, extra map[string]string) (context.Context, integrationSetup) {
	t.Helper()
	env := map[string]string{"MEDIA_DIR": t.TempDir()}
	maps.Copy(env, extra)

	return setupIntegrationWithEnv(t, env)
}

// latestMediaID returns the most recently created media id for a quiz, read
// straight from the store. ListMediaByQuiz orders newest-first, so the first
// row is the latest upload. Slice 2 exposes no list endpoint, so the test reads
// the row through the same store the server writes.
func latestMediaID(ctx context.Context, t *testing.T, stores *store.Stores, quizID int64) int64 {
	t.Helper()
	items, err := stores.Media.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}
	if len(items) == 0 {
		t.Fatalf("no media rows for quiz %d", quizID)
	}

	return items[0].ID
}

// uploadImage posts a multipart image to the quiz's media endpoint and asserts
// the 303-to-quiz-view redirect (slice 2 has no library UI to land on).
func uploadImage(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64, name string, data []byte,
) {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	body, contentType := multipartImage(t, name, data, token)
	req := newMultipartReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media", quizID), body, contentType)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d, want %d; body=%q", got, want, rb)
	}
	if got, want := resp.Header.Get("Location"), fmt.Sprintf("/admin/quizzes/%d", quizID); got != want {
		t.Errorf("upload redirect Location = %q, want %q", got, want)
	}
}

// createQuizWithVisibility posts a quiz with the given visibility and returns
// the id parsed from the redirect Location. Mirrors createQuizAs but adds the
// visibility form field (#103) so a private quiz can be created for the serving
// gate tests.
func createQuizWithVisibility(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, title, visibility string,
) int64 {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes/new")
	form := url.Values{
		"title":       {title},
		"description": {"owned by test"},
		"visibility":  {visibility},
		"csrf_token":  {token},
	}
	req := newFormReq(ctx, t, baseURL+"/admin/quizzes", form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create quiz err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create quiz status = %d, want %d; body=%q", got, want, body)
	}
	loc := resp.Header.Get("Location")
	const prefix = "/admin/quizzes/"
	if !strings.HasPrefix(loc, prefix) {
		t.Fatalf("create quiz Location = %q, want prefix %q", loc, prefix)
	}
	var id int64
	if _, err := fmt.Sscanf(loc[len(prefix):], "%d", &id); err != nil {
		t.Fatalf("parse quiz id from Location %q err = %v", loc, err)
	}

	return id
}

// pngBytes renders a varied-colour PNG of the given size for use as upload
// input. A real decodable image is required: the pipeline rejects anything it
// cannot decode, so a byte blob would not exercise the success path.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode err = %v, want nil", err)
	}

	return buf.Bytes()
}

// multipartImage builds a multipart body carrying the image under the "image"
// field plus the csrf_token field, returning the body and its content type.
func multipartImage(t *testing.T, filename string, data []byte, token string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("CreateFormFile err = %v, want nil", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write image part err = %v, want nil", err)
	}
	if err := mw.WriteField("csrf_token", token); err != nil {
		t.Fatalf("WriteField err = %v, want nil", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close err = %v, want nil", err)
	}

	return &buf, mw.FormDataContentType()
}

// newMultipartReq builds a POST request carrying a multipart body with the
// given content type.
func newMultipartReq(
	ctx context.Context, t *testing.T, target string, body io.Reader, contentType string,
) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", contentType)

	return req
}
