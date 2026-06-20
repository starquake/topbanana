package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/htmx"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/render"
)

// MediaDescriptionService is the slice of the media service the description-edit
// handler needs (#1072): read one row to enforce the IDOR / type guards, and set
// its description label. Defined consumer-side; *media.Service satisfies it.
type MediaDescriptionService interface {
	Get(ctx context.Context, id int64) (*media.Media, error)
	UpdateDescription(ctx context.Context, id int64, description string) error
}

// soundDescriptionFormField carries the new description label submitted from the
// audio library's inline edit.
const soundDescriptionFormField = "description"

// soundDescriptionData backs the sound_description partial for the htmx swap. It
// mirrors the fields the partial reads from a MediaCardData (the inline-render
// path) - the owning quiz and media ids build the action URL, and Description is
// the saved label - so the partial renders identically from either source.
type soundDescriptionData struct {
	QuizID      int64
	ID          int64
	Description string
}

// HandleMediaDescriptionSave updates the host-supplied description label of an
// audio clip for POST /admin/quizzes/{quizID}/media/{mediaID}/description (#1072).
// requireGameHost gates to Host/Admin upstream; this handler adds the per-quiz
// creator-or-admin edit gate plus the IDOR guard that the media belongs to
// {quizID}. The label is editable only on audio rows: a mismatched quiz or a
// non-audio media id answers 404, indistinguishable from a missing one.
//
// On success an htmx request gets the re-rendered description region (an
// outerHTML swap that lands the saved label without a full reload); a plain form
// submit redirects 303 back to the quiz view's sounds section.
func HandleMediaDescriptionSave(
	logger *slog.Logger, csrfMgr *csrf.Manager, svc MediaDescriptionService, quizStore quiz.Store,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		mediaID, ok := handlers.ParseIDFromPath(w, r, logger, "mediaID")
		if !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		if !soundBelongsToQuiz(w, r, logger, csrfMgr, svc, mediaID, quizID) {
			return
		}

		description := r.PostFormValue(soundDescriptionFormField)
		if err := svc.UpdateDescription(r.Context(), mediaID, description); err != nil {
			if errors.Is(err, media.ErrMediaNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error updating media description", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		writeDescriptionResponse(w, r, logger, csrfMgr, renderer, svc, quizID, mediaID)
	})
}

// soundBelongsToQuiz loads the media row and reports whether it is an audio clip
// owned by quizID. A missing row, a foreign quiz, or a non-audio type all answer
// 404 so a probe cannot distinguish them; a store error renders 500.
func soundBelongsToQuiz(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager,
	svc MediaDescriptionService, mediaID, quizID int64,
) bool {
	m, err := svc.Get(r.Context(), mediaID)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			http.NotFound(w, r)

			return false
		}
		logger.ErrorContext(r.Context(), "error loading media for description edit", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return false
	}
	if m.QuizID != quizID || m.Type != media.TypeAudio {
		http.NotFound(w, r)

		return false
	}

	return true
}

// writeDescriptionResponse: htmx gets the re-rendered description region for an
// outerHTML swap; a plain form submit gets the 303 back to the sounds section.
// The htmx path re-reads the stored row so the swap shows exactly the persisted
// (normalized) value, not the raw posted one.
func writeDescriptionResponse(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager,
	renderer *render.Renderer, svc MediaDescriptionService, quizID, mediaID int64,
) {
	if !htmx.IsRequest(r) {
		dest := fmt.Sprintf("/admin/quizzes/%d", quizID) + "#sounds"
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // dest is built from an int64 id, not user input

		return
	}

	m, err := svc.Get(r.Context(), mediaID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error reloading media for description swap", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return
	}
	renderer.RenderPartial(w, r, "sound_description", soundDescriptionData{
		QuizID:      quizID,
		ID:          mediaID,
		Description: m.Description,
	})
}
