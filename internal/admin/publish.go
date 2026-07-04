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

// quizPublishData backs quizpublish.gohtml (#1192): the read-only overview a
// host confirms before publishing. Rounds carries the quiz's questions grouped
// into rounds in play order, mirroring the quiz view, so the host can review the
// whole quiz (and its answers) before the lock.
type quizPublishData struct {
	Title  string
	Quiz   *QuizData
	Rounds []RoundViewData
}

// HandleQuizPublishConfirm renders the publish overview + confirm page (#1192):
// every round -> question -> options with the correct option marked, a warning
// that a published quiz cannot be edited, and a Confirm (POST) button. Ownership
// is gated by requireQuizOwner (not requireEditableQuizOwner: publishing is not
// a content edit). An already-published quiz has nothing to confirm, so it
// redirects back to the quiz view.
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

// HandleQuizPublish publishes the quiz (#1192): it flips published to true and
// redirects back to the quiz view. Ownership is gated by requireQuizOwner. Once
// published the quiz is locked from edits (enforced by requireEditableQuizOwner
// on the content-mutating routes).
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

// HandleQuizUnpublish returns a quiz to draft (#1192). It is allowed only until
// a real (non-preview) game has started: once the quiz has real plays it can no
// longer be unpublished, so a played quiz renders a 409. Ownership is gated by
// requireQuizOwner.
func HandleQuizUnpublish(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		// Atomic unpublish-if-unplayed closes the check-then-act race a
		// QuizHasRealPlays read + SetQuizPublished(false) leaves open: a real
		// game starting between the two calls could leave a played quiz as an
		// editable draft (#1192). The quiz already exists (requireQuizOwner
		// loaded it), so no update means it has been played -> 409.
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
