package integration_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestUploadAuthBeforeSpool_Integration pins that auth runs before the multipart
// parse on the upload/import routes (#1173): a cookieless POST gets the auth 303,
// not the parse's 400 or CSRF's 403, so the body is never spooled.
func TestUploadAuthBeforeSpool_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, nil)
	baseURL := setup.BaseURL

	routes := []struct {
		name string
		path string
	}{
		{name: "media upload", path: "/admin/quizzes/1/media"},
		{name: "audio upload", path: "/admin/quizzes/1/media/audio"},
		{name: "archive import", path: "/admin/quizzes/import/archive"},
	}
	for _, route := range routes {
		t.Run(route.name+" route rejects a cookieless caller before parsing the body", func(t *testing.T) {
			t.Parallel()

			// Keep the 303 rather than following it to /login.
			anon := newAnonClient(t)
			anon.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}

			// A valid body: a 303 (not the CSRF 403) proves auth ran before parse.
			body, contentType := multipartImage(t, "probe.png", pngBytes(t, 8, 8), "not-a-real-token")
			req := newMultipartReq(ctx, t, baseURL+route.path, body, contentType)
			resp, err := anon.Do(req)
			if err != nil {
				t.Fatalf("Do err = %v, want nil", err)
			}
			defer closeBody(t, resp.Body)

			if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
				t.Errorf(
					"cookieless %s status = %d, want %d (login redirect, auth before parse)",
					route.name, got, want,
				)
			}
			if got := resp.Header.Get("Location"); !strings.HasPrefix(got, "/login") {
				t.Errorf("cookieless %s Location = %q, want a /login redirect", route.name, got)
			}
		})
	}
}

// TestSlowUploadResponseWrites_Integration pins that a body streamed slower than
// the WriteTimeout still gets its 303 written and stores exactly one media row
// (#1172). The default 10s WriteTimeout is kept, not shrunk, so the bcrypt-heavy
// setup requests do not flake under coverage load.
func TestSlowUploadResponseWrites_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "slow-boss@example.test",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "slow-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "slow-owner")
	makeHost(ctx, t, setup.DBURI, "slow-owner")
	quizID := createQuizAs(ctx, t, owner, baseURL, "Slow Upload Quiz")

	token := fetchCSRFToken(ctx, t, owner, baseURL+"/admin/quizzes")
	body, contentType := multipartImage(t, "slow.png", pngBytes(t, 64, 64), token)

	// Stream over ~13s, past the 10s WriteTimeout.
	slow := newSlowReader(body.Bytes(), 13*time.Second, 26)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+fmt.Sprintf("/admin/quizzes/%d/media", quizID), slow,
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := owner.Do(req)
	if err != nil {
		t.Fatalf("slow upload Do err = %v, want nil (write deadline must be extended)", err)
	}
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("slow upload status = %d, want %d; body=%q", got, want, rb)
	}
	want := fmt.Sprintf("/admin/quizzes/%d?uploaded=1&failed=0&cancelled=0#images", quizID)
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("slow upload redirect Location = %q, want %q", got, want)
	}

	items, err := setup.Stores.Media.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}
	if got, want := len(items), 1; got != want {
		t.Errorf("stored media rows = %d, want %d (a failed response write plus retry would duplicate)", got, want)
	}
}

// slowReader emits data in fixed-size chunks, sleeping before each so the body
// takes at least the requested duration to stream.
type slowReader struct {
	data  []byte
	off   int
	chunk int
	delay time.Duration
}

// newSlowReader streams data across at least total, split into chunks slices.
func newSlowReader(data []byte, total time.Duration, chunks int) *slowReader {
	chunk := max((len(data)+chunks-1)/chunks, 1)

	return &slowReader{
		data:  data,
		chunk: chunk,
		delay: total / time.Duration(chunks),
	}
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.off >= len(s.data) {
		return 0, io.EOF
	}
	// Sleep before every chunk so total stream time is chunks*delay.
	time.Sleep(s.delay)

	end := min(s.off+s.chunk, len(s.data))
	n := copy(p, s.data[s.off:end])
	s.off += n

	return n, nil
}
