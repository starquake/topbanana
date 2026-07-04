package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
)

func publishRequest(t *testing.T, method, path string, quizID int64) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.SetPathValue("quizID", strconv.FormatInt(quizID, 10))

	return withTestAdmin(req)
}

func TestHandleQuizPublish(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("publishes a draft and redirects to the quiz view", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Draft", "draft"))

		handler := HandleQuizPublish(logger, nil, env.quizzes)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodPost, "/admin/quizzes/1/publish", qz.ID))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes/"+strconv.FormatInt(qz.ID, 10); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		updated, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if !updated.Published {
			t.Error("quiz Published = false after publish, want true")
		}
	})
}

func TestHandleQuizUnpublish(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns a never-played published quiz to draft", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, publishedTwoQuestionQuiz("Pub", "pub"))

		handler := HandleQuizUnpublish(logger, nil, env.quizzes)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodPost, "/admin/quizzes/1/unpublish", qz.ID))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		updated, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if updated.Published {
			t.Error("quiz Published = true after unpublish, want false")
		}
	})

	t.Run("is blocked once the quiz has a real play", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, publishedTwoQuestionQuiz("Played", "played"))
		player := env.seedPlayer(t, "alice")
		env.playThrough(t, qz, player)

		handler := HandleQuizUnpublish(logger, nil, env.quizzes)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodPost, "/admin/quizzes/1/unpublish", qz.ID))

		if got, want := rr.Code, http.StatusConflict; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		updated, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if !updated.Published {
			t.Error("quiz Published = false after blocked unpublish, want true (unchanged)")
		}
	})
}

func TestHandleQuizPublishConfirm(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("renders the overview for a draft", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Draft", "draft"))

		handler := HandleQuizPublishConfirm(logger, nil, env.quizzes)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodGet, "/admin/quizzes/1/publish", qz.ID))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got := rr.Body.String(); !strings.Contains(got, "locked from edits") {
			t.Error("confirm page missing the edit-lock warning")
		}
	})

	t.Run("redirects to the quiz view when already published", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, publishedTwoQuestionQuiz("Pub", "pub"))

		handler := HandleQuizPublishConfirm(logger, nil, env.quizzes)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodGet, "/admin/quizzes/1/publish", qz.ID))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}

// TestPublishedQuiz_EditLock pins that a published quiz rejects content edits
// with 409 through requireEditableQuizOwner (#1192): the mode toggle and the
// delete route stand in for the whole content-mutating cluster.
func TestPublishedQuiz_EditLock(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("mode toggle on a published quiz is blocked", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, publishedTwoQuestionQuiz("Pub", "pub"))

		handler := HandleQuizSetMode(logger, nil, env.quizzes)
		req := publishRequest(t, http.MethodPost, "/admin/quizzes/1/mode/live", qz.ID)
		req.SetPathValue("mode", "live")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusConflict; got != want {
			t.Errorf("mode toggle status = %d, want %d", got, want)
		}
	})

	t.Run("delete of a published quiz is blocked", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, publishedTwoQuestionQuiz("Pub", "pub"))

		handler := HandleQuizDelete(logger, nil, env.quizzes, noopMediaRemover{})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, publishRequest(t, http.MethodPost, "/admin/quizzes/1/delete", qz.ID))

		if got, want := rr.Code, http.StatusConflict; got != want {
			t.Errorf("delete status = %d, want %d", got, want)
		}
		if _, err := env.quizzes.GetQuiz(t.Context(), qz.ID); err != nil {
			t.Errorf("quiz missing after blocked delete: %v", err)
		}
	})
}

// noopMediaRemover satisfies admin.QuizMediaRemover for the delete handler;
// the edit-lock rejects the request before any media removal runs.
type noopMediaRemover struct{}

func (noopMediaRemover) RemoveQuizDir(_ int64) error { return nil }
