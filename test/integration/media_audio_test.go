package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// mp3Bytes returns a payload whose leading bytes sniff as MP3 via the "ID3"
// tag. The server does not decode audio, so a magic-byte prefix is enough to
// exercise the accept path; the body length is padded to a few KB so a Range
// request has something to split.
func mp3Bytes() []byte {
	return append([]byte("ID3"), bytes.Repeat([]byte{0x00}, 4096)...)
}

// TestMediaAudioUpload_Integration covers the audio upload endpoint (#1059): an
// owner can upload a sound to their editable quiz, the sound then serves back as
// audio/mpeg and honours a Range request; an unsupported format and an over-cap
// upload are both rejected with 400; and a non-owner host is refused.
func TestMediaAudioUpload_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS":          "audio-boss@example.test",
		"MEDIA_AUDIO_MAX_BYTES": strconv.Itoa(8192),
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-owner")
	other := registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-other")
	makeHost(ctx, t, setup.DBURI, "audio-owner")
	makeHost(ctx, t, setup.DBURI, "audio-other")

	t.Run("owner uploads then serves back with a duration", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Duration Quiz")
		uploadAudio(ctx, t, owner, baseURL, quizID, "clip.mp3", mp3Bytes(), 95000)
		row := latestMedia(ctx, t, setup.Stores, quizID)
		if got, want := row.Type, media.TypeAudio; got != want {
			t.Errorf("stored media Type = %q, want %q", got, want)
		}
		if row.DurationMs == nil {
			t.Fatal("stored media DurationMs = nil, want the posted 95000")
		}
		if got, want := *row.DurationMs, 95000; got != want {
			t.Errorf("stored media DurationMs = %d, want %d", got, want)
		}

		resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d", row.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("serve status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "audio/mpeg"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		if got, want := resp.Header.Get("Accept-Ranges"), "bytes"; got != want {
			t.Errorf("Accept-Ranges = %q, want %q (range needed for seeking)", got, want)
		}
	})

	t.Run("range request returns 206 with the requested slice", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Range Quiz")
		uploadAudio(ctx, t, owner, baseURL, quizID, "seek.mp3", mp3Bytes(), 0)
		mediaID := latestMedia(ctx, t, setup.Stores, quizID).ID

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+fmt.Sprintf("/media/%d", mediaID), nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.Header.Set("Range", "bytes=0-99")
		resp, err := owner.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusPartialContent; got != want {
			t.Fatalf("range status = %d, want %d", got, want)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body err = %v, want nil", err)
		}
		if got, want := len(body), 100; got != want {
			t.Errorf("range body len = %d, want %d", got, want)
		}
	})

	t.Run("unsupported format is 400", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Bad Format Quiz")
		status, _ := uploadAudioRaw(ctx, t, owner, baseURL, quizID, "note.txt", []byte("not audio at all"))
		if got, want := status, http.StatusBadRequest; got != want {
			t.Errorf("unsupported audio status = %d, want %d", got, want)
		}
	})

	t.Run("over-cap upload is 400", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Over Cap Quiz")
		big := append([]byte("ID3"), bytes.Repeat([]byte{0x00}, 16384)...)
		status, body := uploadAudioRaw(ctx, t, owner, baseURL, quizID, "big.mp3", big)
		if got, want := status, http.StatusBadRequest; got != want {
			t.Errorf("over-cap audio status = %d, want %d", got, want)
		}
		if got, want := string(body), "maximum upload size"; !strings.Contains(got, want) {
			t.Errorf("over-cap body = %q, should contain %q", got, want)
		}
	})

	t.Run("non-owner host is refused", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Owner Gate Quiz")
		token := fetchCSRFToken(ctx, t, other, baseURL+"/admin/quizzes")
		body, contentType := multipartAudio(t, "clip.mp3", mp3Bytes(), 0, token)
		req := newMultipartReq(
			ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media/audio", quizID), body, contentType,
		)
		resp, err := other.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("non-owner audio upload status = %d, want %d", got, want)
		}
	})
}

// TestMediaAudioQuizCap_Integration pins the per-quiz library ceiling on the
// audio route (#1059): the cap is per-type, so an over-cap audio upload returns
// 409 with a message naming "audio" (not "image"). Runs with its own setupMedia
// and a tiny MEDIA_QUIZ_IMAGE_LIMIT (with a generous budget so the budget guard
// does not fire first).
func TestMediaAudioQuizCap_Integration(t *testing.T) {
	t.Parallel()

	const limit = 2
	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS":           "audio-cap-boss@example.test",
		"MEDIA_QUIZ_IMAGE_LIMIT": strconv.Itoa(limit),
		"MEDIA_UPLOAD_BUDGET":    "1000",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-cap-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-cap-owner")
	makeHost(ctx, t, setup.DBURI, "audio-cap-owner")

	quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Cap Quiz")
	for i := range limit {
		uploadAudio(ctx, t, owner, baseURL, quizID, fmt.Sprintf("clip%d.mp3", i), mp3Bytes(), 0)
	}

	status, body := uploadAudioRaw(ctx, t, owner, baseURL, quizID, "one-too-many.mp3", mp3Bytes())
	if got, want := status, http.StatusConflict; got != want {
		t.Errorf("over-cap audio status = %d, want %d", got, want)
	}
	if got, want := string(body), "audio limit"; !strings.Contains(got, want) {
		t.Errorf("over-cap audio body = %q, should contain %q", got, want)
	}
}

// TestMediaQuizCapIsPerType_Integration pins that the per-quiz library ceiling is
// type-scoped (#1059): audio uploads do not draw down the image ceiling and
// images do not draw down the audio ceiling. With the limit set to 1, a quiz can
// hold one image and one audio at once, and the second upload of either type is
// the one rejected with 409.
func TestMediaQuizCapIsPerType_Integration(t *testing.T) {
	t.Parallel()

	const limit = 1
	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS":           "per-type-boss@example.test",
		"MEDIA_QUIZ_IMAGE_LIMIT": strconv.Itoa(limit),
		"MEDIA_UPLOAD_BUDGET":    "1000",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "per-type-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "per-type-owner")
	makeHost(ctx, t, setup.DBURI, "per-type-owner")

	quizID := createQuizAs(ctx, t, owner, baseURL, "Per Type Cap Quiz")

	// One image fills the image ceiling but leaves the audio ceiling open.
	uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 64, 64))
	uploadAudio(ctx, t, owner, baseURL, quizID, "clip.mp3", mp3Bytes(), 0)

	imgStatus, _, _ := uploadOneFile(ctx, t, owner, baseURL, quizID, "second.png")
	if got, want := imgStatus, http.StatusConflict; got != want {
		t.Errorf("second image status = %d, want %d (image ceiling full)", got, want)
	}

	audioStatus, _ := uploadAudioRaw(ctx, t, owner, baseURL, quizID, "second.mp3", mp3Bytes())
	if got, want := audioStatus, http.StatusConflict; got != want {
		t.Errorf("second audio status = %d, want %d (audio ceiling full)", got, want)
	}
}

// TestMediaAudioLibraryAndPicker_Integration covers the sounds library on the
// quiz view and the question editor's audio picker (#1059): an uploaded sound
// shows with its duration label and an audio preview; the question editor lists
// it as a radio option; and attaching it persists audio_media_id.
func TestMediaAudioLibraryAndPicker_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "audio-lib-boss@example.test",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-lib-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "audio-lib-owner")
	makeHost(ctx, t, setup.DBURI, "audio-lib-owner")

	quizID := createQuizAs(ctx, t, owner, baseURL, "Audio Picker Quiz")
	questionID := addQuestionToQuiz(ctx, t, setup.Stores, quizID, "Name that tune")

	uploadAudio(ctx, t, owner, baseURL, quizID, "tune.mp3", mp3Bytes(), 95000)
	soundID := latestMedia(ctx, t, setup.Stores, quizID).ID

	t.Run("quiz view lists the sound with a duration label", func(t *testing.T) {
		t.Parallel()
		page := getQuizViewBody(ctx, t, owner, baseURL, quizID)
		for _, want := range []string{
			`data-testid="audio-library-item"`,
			fmt.Sprintf(`src="/media/%d"`, soundID),
			`data-testid="audio-duration"`,
			"1:35",
			fmt.Sprintf(`action="/admin/quizzes/%d/media/audio"`, quizID),
		} {
			if !strings.Contains(page, want) {
				t.Errorf("quiz view missing %q", want)
			}
		}
	})

	t.Run("question editor shows the audio picker and attaches the sound", func(t *testing.T) {
		t.Parallel()
		editURL := fmt.Sprintf("%s/admin/quizzes/%d/questions/%d/edit", baseURL, quizID, questionID)
		page := getPageBody(ctx, t, owner, editURL)
		for _, want := range []string{`data-testid="question-audio-picker"`, `name="audio_media_id"`} {
			if !strings.Contains(page, want) {
				t.Errorf("question editor missing %q", want)
			}
		}

		saveQuestionWithAudio(ctx, t, owner, baseURL, quizID, questionID, soundID)

		saved, err := setup.Stores.Quizzes.GetQuestion(ctx, questionID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if saved.AudioMediaID == nil {
			t.Fatal("saved question AudioMediaID = nil, want the attached sound id")
		}
		if got, want := *saved.AudioMediaID, soundID; got != want {
			t.Errorf("saved question AudioMediaID = %d, want %d", got, want)
		}
	})
}

// addQuestionToQuiz inserts a single question into the quiz via the store and
// returns its id. It seeds the quiz's question through the same transactional
// path the admin handler uses, so the audio-picker test can edit a real
// question without driving the question-create form.
func addQuestionToQuiz(ctx context.Context, t *testing.T, stores *store.Stores, quizID int64, text string) int64 {
	t.Helper()
	qs := &quiz.Question{
		QuizID:  quizID,
		Text:    text,
		Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}},
	}
	if err := stores.Quizzes.CreateQuestionAtNextPosition(ctx, qs); err != nil {
		t.Fatalf("CreateQuestionAtNextPosition err = %v, want nil", err)
	}

	return qs.ID
}

// getPageBody fetches a page and returns its body, asserting a 200. Used to
// probe the question editor's rendered HTML for the audio picker.
func getPageBody(ctx context.Context, t *testing.T, client *http.Client, target string) string {
	t.Helper()
	resp := httpGet(ctx, t, client, target)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET %s status = %d, want %d", target, got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body err = %v, want nil", err)
	}

	return string(body)
}

// latestMedia returns the most recently created media row for a quiz, read
// straight from the store (newest first).
func latestMedia(ctx context.Context, t *testing.T, stores *store.Stores, quizID int64) *media.Media {
	t.Helper()
	items, err := stores.Media.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}
	if len(items) == 0 {
		t.Fatalf("no media rows for quiz %d", quizID)
	}

	return items[0]
}

// uploadAudio posts a single-file audio upload to the quiz's audio endpoint and
// asserts the 303-to-quiz-view redirect lands on the sounds section anchor.
func uploadAudio(
	ctx context.Context, t *testing.T, client *http.Client,
	baseURL string, quizID int64, name string, data []byte, durationMs int,
) {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	body, contentType := multipartAudio(t, name, data, durationMs, token)
	req := newMultipartReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media/audio", quizID), body, contentType)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("audio upload Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("audio upload status = %d, want %d; body=%q", got, want, rb)
	}
	want := fmt.Sprintf("/admin/quizzes/%d#sounds", quizID)
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("audio upload redirect Location = %q, want %q", got, want)
	}
}

// uploadAudioRaw posts a single-file audio upload and returns the status and
// body without asserting an outcome, for the rejection-path tests. The duration
// field is omitted: every rejection path ignores it.
func uploadAudioRaw(
	ctx context.Context, t *testing.T, client *http.Client,
	baseURL string, quizID int64, name string, data []byte,
) (status int, body []byte) {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	reqBody, contentType := multipartAudio(t, name, data, 0, token)
	req := newMultipartReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/media/audio", quizID), reqBody, contentType)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("audio upload Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read audio upload body err = %v, want nil", err)
	}

	return resp.StatusCode, read
}

// multipartAudio builds a multipart body carrying the audio under the "audio"
// field plus the csrf_token and (when positive) duration_ms fields.
func multipartAudio(t *testing.T, filename string, data []byte, durationMs int, token string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("audio", filename)
	if err != nil {
		t.Fatalf("CreateFormFile err = %v, want nil", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write audio part err = %v, want nil", err)
	}
	if durationMs > 0 {
		if err := mw.WriteField("duration_ms", strconv.Itoa(durationMs)); err != nil {
			t.Fatalf("WriteField duration_ms err = %v, want nil", err)
		}
	}
	if err := mw.WriteField("csrf_token", token); err != nil {
		t.Fatalf("WriteField csrf_token err = %v, want nil", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close err = %v, want nil", err)
	}

	return &buf, mw.FormDataContentType()
}

// saveQuestionWithAudio posts the question edit form with the audio_media_id set
// to soundID, preserving the question's text and a minimal valid option set, and
// asserts the 303 redirect back to the quiz view.
func saveQuestionWithAudio(
	ctx context.Context, t *testing.T, client *http.Client,
	baseURL string, quizID, questionID, soundID int64,
) {
	t.Helper()
	saveURL := baseURL + fmt.Sprintf("/admin/quizzes/%d/questions/%d", quizID, questionID)
	token := fetchCSRFToken(ctx, t, client, saveURL+"/edit")
	form := url.Values{
		"csrf_token":        {token},
		"id":                {strconv.FormatInt(questionID, 10)},
		"text":              {"Name that tune"},
		"audio_media_id":    {strconv.FormatInt(soundID, 10)},
		"option[0].text":    {"A"},
		"option[0].correct": {"on"},
		"option[1].text":    {"B"},
	}
	req := newFormReq(ctx, t, saveURL, form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("save question Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("save question status = %d, want %d; body=%q", got, want, rb)
	}
}
