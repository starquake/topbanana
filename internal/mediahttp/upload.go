package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

const (
	// uploadFormField is the multipart field the image arrives under.
	uploadFormField = "image"

	// maxUploadRequestBytes caps the whole multipart request body. It sits
	// above media.MaxUploadBytes (the ~10 MB image cap the pipeline enforces on
	// the file part) so the multipart envelope - boundaries, the csrf_token
	// field, headers - fits without tripping the body cap before the pipeline
	// can return the cleaner ErrUploadTooLarge for an oversized image. The
	// pipeline still rejects an image part over MaxUploadBytes, so this is only
	// a coarse outer guard, not the real image limit.
	maxUploadRequestBytes = media.MaxUploadBytes + multipartEnvelopeHeadroom

	// multipartEnvelopeHeadroom is the slack added over the image cap to cover
	// the multipart envelope (boundaries, the csrf_token field, part headers).
	multipartEnvelopeHeadroom = 2 << 20

	// multipartMemoryBytes is how much of the parsed multipart form is buffered
	// in memory before parts spill to temp files. Kept modest so a flood of
	// concurrent uploads does not pin large buffers; the file part is streamed
	// to the pipeline either way.
	multipartMemoryBytes = 1 << 20
)

// QuizEditLookup is the slice of the quiz store the upload handler uses to
// enforce the per-quiz edit gate: a host may upload only to a quiz they
// created, an admin to any (mirrors admin.canEditQuiz / requireQuizOwner).
type QuizEditLookup interface {
	// GetQuiz returns a quiz (including CreatedByPlayerID). Returns
	// quiz.ErrQuizNotFound when the quiz does not exist.
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
}

// HandleMediaUpload accepts a multipart image upload for POST
// /admin/quizzes/{quizID}/media and stores it through the media service. The
// route is host/admin-gated upstream (requireGameHost); this handler adds the
// per-quiz edit gate so a host may upload only to a quiz they created and an
// admin to any (the same creator-or-admin rule the admin question/quiz
// mutations apply via requireQuizOwner). A non-owner host gets a 403; a missing
// quiz a 404. Pipeline rejections (too large, empty, unsupported) map to 400;
// other failures to 500. On success it redirects 303 to the quiz view - the
// library UI that would render the new image lands in a later slice (#936
// slice 3).
//
// The caller is expected to front this handler with MaxMultipartFormMiddleware
// so the body is capped and the multipart form is parsed before the CSRF
// middleware validates the token, which for a multipart form lives in the
// parsed PostForm.
func HandleMediaUpload(logger *slog.Logger, svc MediaService, quizzes QuizEditLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			// The host gate upstream guarantees a player on the context; a
			// missing one is a wiring bug, not a client error.
			logger.ErrorContext(r.Context(), "media upload reached handler without a player on context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !authorizeQuizEdit(w, r, logger, quizzes, quizID, player) {
			return
		}

		ctx := r.Context()
		file, _, err := r.FormFile(uploadFormField)
		if err != nil {
			http.Error(w, "missing image file", http.StatusBadRequest)

			return
		}
		defer func() {
			if cerr := file.Close(); cerr != nil {
				logger.ErrorContext(ctx, "error closing uploaded file", slog.Any("err", cerr))
			}
		}()

		if _, err = svc.Store(ctx, quizID, player.ID, file); err != nil {
			writeUploadError(w, r, logger, err)

			return
		}

		// quizID is an int64 parsed from the path, so the formatted target is a
		// fixed-shape internal admin path with no caller-controlled segment.
		dest := fmt.Sprintf("/admin/quizzes/%d", quizID)
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // dest is built from an int64 id, not user input
	})
}

// authorizeQuizEdit loads the quiz and gates the request on the creator-or-admin
// edit rule (mirrors admin.canEditQuiz): the player must be the quiz's creator
// or an admin. A missing quiz yields 404, a non-owner non-admin a 403. Returns
// whether to proceed.
func authorizeQuizEdit(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, quizzes QuizEditLookup, quizID int64, player *auth.Player,
) bool {
	qz, err := quizzes.GetQuiz(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return false
		}
		logger.ErrorContext(r.Context(), "error loading quiz for media upload gate", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return false
	}

	if !player.IsAdmin() && player.ID != qz.CreatedByPlayerID {
		http.Error(w, "you do not have permission to upload media to this quiz", http.StatusForbidden)

		return false
	}

	return true
}

// writeUploadError maps a media.Service.Store error to an HTTP response: the
// pipeline's caller-fault sentinels become 400 with a short message, anything
// else is logged and returned as 500.
func writeUploadError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, media.ErrUploadTooLarge):
		http.Error(w, "image exceeds the maximum upload size", http.StatusBadRequest)
	case errors.Is(err, media.ErrImageTooLarge):
		http.Error(w, "image dimensions exceed the maximum", http.StatusBadRequest)
	case errors.Is(err, media.ErrEmptyUpload):
		http.Error(w, "image file is empty", http.StatusBadRequest)
	case errors.Is(err, media.ErrUnsupportedImage):
		http.Error(w, "unsupported image format (use jpg, png, or webp)", http.StatusBadRequest)
	default:
		logger.ErrorContext(r.Context(), "error storing uploaded media", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// MaxMultipartFormMiddleware caps the request body at maxUploadRequestBytes and
// parses the multipart form before the next handler runs. It is mounted in
// front of the CSRF middleware on the upload route: the CSRF validator reads the
// token from PostForm, which for a multipart submission is only populated once
// the multipart body has been parsed. Parsing here (with the large cap) makes
// the token visible to the CSRF layer and leaves the parsed file part ready for
// the handler's FormFile call. A body over the cap or a malformed form yields
// 400 before the token check, so an oversized upload cannot slip past as a CSRF
// failure.
func MaxMultipartFormMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
		// MaxBytesReader above bounds the body, so the parse cannot exhaust
		// memory despite gosec's G120 flag on the bare ParseMultipartForm call.
		if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil { //nolint:gosec // body capped above
			http.Error(w, "invalid or oversized upload", http.StatusBadRequest)

			return
		}
		// Parts larger than multipartMemoryBytes spill to temp files that
		// net/http never removes on its own; clean them up once the handler is
		// done reading the upload (best-effort).
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		next.ServeHTTP(w, r)
	})
}
