package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
)

// quizPublishData backs quizpublish.gohtml: the read-only pre-publish review (#1192).
type quizPublishData struct {
	Title  string
	Quiz   *QuizData
	Rounds []RoundViewData
}

// HandleQuizPublishConfirm renders the pre-publish review and confirm page for a draft quiz (#1192); an already-published quiz redirects to the quiz view.
func HandleQuizPublishConfirm(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizpublish.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		if qz.Published {
			http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)

			return
		}

		rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)
		renderer.Render(w, r, http.StatusOK, quizPublishData{
			Title:  "Admin Dashboard - Publish Quiz",
			Quiz:   quizData,
			Rounds: buildRoundView(rounds, quizData.Questions),
		})
	})
}

// HandleQuizPublish flips the quiz to published and redirects to the quiz view; publishing then locks it from content edits (#1192).
func HandleQuizPublish(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		if err := quizStore.SetQuizPublished(r.Context(), quizID, true); err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error publishing quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuizUnpublish returns a quiz to draft, allowed only until a real (non-preview) game has started; a played quiz renders a 409 (#1192).
func HandleQuizUnpublish(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		// Atomic guard against the check-then-act race: the quiz is loaded, so no update means it has been played -> 409.
		unpublished, err := quizStore.UnpublishQuizIfUnplayed(r.Context(), quizID)
		if err != nil {
			logger.ErrorContext(r.Context(), "error unpublishing quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}
		if !unpublished {
			render409(w, r, logger, csrfMgr,
				"This quiz has been played and can no longer be unpublished.")

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}
