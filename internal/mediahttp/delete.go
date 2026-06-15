package mediahttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/htmx"
	"github.com/starquake/topbanana/internal/media"
)

// HandleMediaDelete deletes a stored image for POST
// /admin/quizzes/{quizID}/media/{mediaID}/delete. The route is host/admin-gated
// upstream (requireGameHost); this handler adds the per-quiz creator-or-admin
// edit gate (the same rule HandleMediaUpload applies), so a host may delete only
// from a quiz they created and an admin from any. This is also the admin
// image-moderation path (#936): an admin passes the edit gate on every quiz, so
// the per-quiz library delete doubles as cross-quiz moderation.
//
// The {mediaID} in the path is checked against the loaded row's QuizID: a media
// row whose quiz is not the gated {quizID} yields 404 (an IDOR guard so the
// owner of quiz A cannot delete quiz B's image by posting to A's path with B's
// media id). A non-owner non-admin gets 403; a missing quiz or a missing /
// foreign media id a 404. On success it redirects 303 back to the quiz view's
// images section so the page does not jump to the top.
func HandleMediaDelete(logger *slog.Logger, svc MediaService, quizzes QuizEditLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		mediaID, ok := handlers.ParseIDFromPath(w, r, logger, "mediaID")
		if !ok {
			return
		}

		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			// The host gate upstream guarantees a player on the context; a
			// missing one is a wiring bug, not a client error.
			logger.ErrorContext(r.Context(), "media delete reached handler without a player on context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !authorizeQuizEdit(w, r, logger, quizzes, quizID, player) {
			return
		}

		ctx := r.Context()
		m, err := svc.Get(ctx, mediaID)
		if err != nil {
			if errors.Is(err, media.ErrMediaNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(ctx, "error loading media for delete", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		// IDOR guard: the media must belong to the gated quiz. Without this, the
		// quiz-{quizID} edit gate would pass for the owner of that quiz even when
		// mediaID names an image owned by a different quiz. A mismatch is answered
		// 404 so a foreign id is indistinguishable from a missing one.
		if m.QuizID != quizID {
			http.NotFound(w, r)

			return
		}

		if err = svc.Delete(ctx, mediaID); err != nil {
			if errors.Is(err, media.ErrMediaNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(ctx, "error deleting media", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		writeDeleteResponse(w, r, quizID)
	})
}

// writeDeleteResponse picks the post-delete response: an htmx-driven submit
// expects an empty 200 it can swap into the thumbnail tile; a plain form
// submit gets a 303 to the images section so the page reloads with the row
// gone.
func writeDeleteResponse(w http.ResponseWriter, r *http.Request, quizID int64) {
	if htmx.IsRequest(r) {
		w.WriteHeader(http.StatusOK)

		return
	}

	dest := fmt.Sprintf("/admin/quizzes/%d", quizID) + "#images"
	http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // dest is built from an int64 id, not user input
}
