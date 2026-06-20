package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/media"
)

// newDescriptionHandler builds HandleMediaDescriptionSave over a real media
// service backed by the env's store, so the test exercises the full
// gate/IDOR/update path against a real DB.
func newDescriptionHandler(t *testing.T, env *adminEnv) http.Handler {
	t.Helper()

	svc := media.NewService(env.media, t.TempDir(), 1<<20, 1<<20, slog.New(slog.DiscardHandler))

	return HandleMediaDescriptionSave(slog.New(slog.DiscardHandler), newRoundsCSRF(), svc, env.quizzes)
}

// descriptionRequest builds the POST request for one (quizID, mediaID,
// description) tuple with the path values and form body the handler reads. The
// caller attaches the actor (and, for the htmx case, the Hx-Request header)
// before serving.
func descriptionRequest(
	t *testing.T, quizID, mediaID int64, description string,
) *http.Request {
	t.Helper()

	form := url.Values{"description": {description}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/admin/quizzes/"+strconv.FormatInt(quizID, 10)+"/media/"+strconv.FormatInt(mediaID, 10)+"/description",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("quizID", strconv.FormatInt(quizID, 10))
	req.SetPathValue("mediaID", strconv.FormatInt(mediaID, 10))

	return req
}

// serveDescription serves a prepared request through the handler.
func serveDescription(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandleMediaDescriptionSave(t *testing.T) {
	t.Parallel()

	t.Run("updates the description and redirects on a plain submit", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Sounds", "media-desc-update"))
		mediaID := env.seedAudioMedia(t, qz.ID, "old label")
		handler := newDescriptionHandler(t, env)

		rec := serveDescription(handler, adminActor(descriptionRequest(t, qz.ID, mediaID, "  new label  ")))

		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := rec.Header().Get("Location"), "#sounds"; !strings.Contains(got, want) {
			t.Errorf("Location = %q, should contain %q", got, want)
		}
		m, err := env.media.GetMedia(t.Context(), mediaID)
		if err != nil {
			t.Fatalf("GetMedia err = %v, want nil", err)
		}
		if got, want := m.Description, "new label"; got != want {
			t.Errorf("stored Description = %q, want %q (trimmed)", got, want)
		}
	})

	t.Run("htmx request returns the re-rendered description partial", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Sounds", "media-desc-htmx"))
		mediaID := env.seedAudioMedia(t, qz.ID, "old label")
		handler := newDescriptionHandler(t, env)

		req := descriptionRequest(t, qz.ID, mediaID, "fresh label")
		req.Header.Set("Hx-Request", "true")
		rec := serveDescription(handler, adminActor(req))

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		body := rec.Body.String()
		if want := "fresh label"; !strings.Contains(body, want) {
			t.Errorf("body should contain the saved label %q, got %q", want, body)
		}
		if want := "sound-description-" + strconv.FormatInt(mediaID, 10); !strings.Contains(body, want) {
			t.Errorf("body should contain the swap target id %q", want)
		}
	})

	t.Run("media from another quiz is a 404 (IDOR guard)", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qzA := env.seedQuiz(t, ownedQuiz("Quiz A", "media-desc-idor-a"))
		qzB := env.seedQuiz(t, ownedQuiz("Quiz B", "media-desc-idor-b"))
		mediaB := env.seedAudioMedia(t, qzB.ID, "b label")
		handler := newDescriptionHandler(t, env)

		rec := serveDescription(handler, adminActor(descriptionRequest(t, qzA.ID, mediaB, "x")))

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		m, err := env.media.GetMedia(t.Context(), mediaB)
		if err != nil {
			t.Fatalf("GetMedia err = %v, want nil", err)
		}
		if got, want := m.Description, "b label"; got != want {
			t.Errorf("Description = %q, want %q (unchanged)", got, want)
		}
	})

	t.Run("non-audio media is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Images", "media-desc-image"))
		imageID := env.seedMedia(t, qz.ID)
		handler := newDescriptionHandler(t, env)

		rec := serveDescription(handler, adminActor(descriptionRequest(t, qz.ID, imageID, "x")))

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner is a 403", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Sounds", "media-desc-403"))
		mediaID := env.seedAudioMedia(t, qz.ID, "label")
		handler := newDescriptionHandler(t, env)

		rec := serveDescription(handler, nonOwnerActor(descriptionRequest(t, qz.ID, mediaID, "x")))

		if got, want := rec.Code, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("unknown media id is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Sounds", "media-desc-missing"))
		handler := newDescriptionHandler(t, env)

		rec := serveDescription(handler, adminActor(descriptionRequest(t, qz.ID, 999999, "x")))

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}
