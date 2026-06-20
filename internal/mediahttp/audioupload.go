package mediahttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
)

const (
	// audioUploadFormField is the multipart field an audio upload arrives under.
	// Distinct from the image "images" field so the two upload routes never
	// collide on a shared form name (#1059).
	audioUploadFormField = "audio"

	// audioDurationFormField carries the in-browser-measured clip length in
	// whole milliseconds. It is advisory: audio is not decoded server-side, so a
	// missing or unparseable value stores NULL rather than failing the upload.
	audioDurationFormField = "duration_ms"

	// audioDescriptionFormField carries the host-supplied library label (#1072).
	// The form JS prefills it with the picked file's name; the service falls back
	// to filename without its extension when it is absent (the no-JS path).
	audioDescriptionFormField = "description"
)

// HandleAudioUpload accepts a single-file multipart audio upload for POST
// /admin/quizzes/{quizID}/media/audio and stores it through the media service.
// Unlike the image route this is one file per request: the form JS measures the
// clip's duration in the browser and posts it alongside the file, and there is
// no batch / partial-success shape.
//
// The route is host/admin-gated upstream (requireGameHost); this handler adds
// the per-quiz creator-or-admin edit gate, the per-quiz library ceiling, and the
// per-host upload budget, so an audio upload draws on the same abuse backstops an
// image upload does (#988). The order is: library-size cap (409), then budget
// charge (429), then store - so a 409 never leaves a charge behind.
//
// On success a plain form submit redirects 303 back to the quiz view's audio
// section; a client that sets Accept: application/json gets the stored row's id
// so the progressive-enhancement JS can render its own row.
func HandleAudioUpload(
	logger *slog.Logger, svc MediaService, quizzes QuizEditLookup,
	budget *UploadBudgetLimiter, quizImageLimit int,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "audio upload reached handler without a player on context")
			http.Error(w, internalErrorMessage, http.StatusInternalServerError)

			return
		}

		if !authorizeQuizEdit(w, r, logger, quizzes, quizID, player) {
			return
		}

		file := audioUploadFile(r)
		if file == nil {
			http.Error(w, "missing audio file", http.StatusBadRequest)

			return
		}

		// Library-size cap before the budget charge: a quiz at its ceiling is a
		// clear admin denial (409), not abuse, so it must not draw down the
		// host's rate budget. The cap is per-type, so audio counts only against
		// audio rows, not the image ceiling (#1059).
		if !checkQuizMediaLimit(w, r, logger, svc, quizID, 1, quizImageLimit, media.TypeAudio) {
			return
		}

		if allowed, retryAfter := budget.Charge(player.ID, 1); !allowed {
			writeRateLimited(w, retryAfter)

			return
		}

		desc := r.PostFormValue(audioDescriptionFormField)
		mediaID, err := storeOneAudio(r.Context(), svc, quizID, player.ID, audioDurationFromForm(r), desc, file)
		if err != nil {
			writeAudioUploadResult(w, r, logger, quizID, mediaID, err)

			return
		}

		writeAudioUploadResult(w, r, logger, quizID, mediaID, nil)
	})
}

// audioUploadFile returns the single file part under the "audio" field, or nil
// when the request carries none.
func audioUploadFile(r *http.Request) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	files := r.MultipartForm.File[audioUploadFormField]
	if len(files) == 0 {
		return nil
	}

	return files[0]
}

// audioDurationFromForm reads the in-browser-measured duration_ms field. A
// missing, negative, or unparseable value yields 0, which the service stores as
// NULL: the duration is advisory, so a client that omits it never fails the
// upload.
func audioDurationFromForm(r *http.Request) int {
	raw := r.PostFormValue(audioDurationFormField)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// storeOneAudio opens the file part and stores it via media.Service.StoreAudio.
// The ctx.Err check is the only abort point: StoreAudio is not context-aware, so
// a cancel that arrived before the store is surfaced here.
func storeOneAudio(
	ctx context.Context, svc MediaService, quizID, playerID int64, durationMs int, description string,
	header *multipart.FileHeader,
) (mediaID int64, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("upload cancelled before store of %q: %w", header.Filename, ctxErr)
	}
	f, err := header.Open()
	if err != nil {
		return 0, fmt.Errorf("opening audio upload part %q: %w", header.Filename, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing audio upload part %q: %w", header.Filename, cerr)
		}
	}()

	stored, err := svc.StoreAudio(ctx, quizID, playerID, durationMs, description, header.Filename, f)
	if err != nil {
		return 0, fmt.Errorf("storing audio upload part %q: %w", header.Filename, err)
	}

	return stored.ID, nil
}

// audioUploadResultJSON is the wire shape of a successful audio upload. The id
// names the new audio row (also the /media/{id} URL suffix).
type audioUploadResultJSON struct {
	ID int64 `json:"id"`
}

// writeAudioUploadResult maps a store outcome to the response. On a pipeline
// rejection it surfaces a precise 4xx (there is no batch banner to fall back
// to); a JSON client gets the id on success or a plain-text reason on failure.
func writeAudioUploadResult(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger, quizID, mediaID int64, err error,
) {
	if err != nil {
		writeAudioUploadError(w, r, logger, err)

		return
	}

	if wantsJSON(r) {
		writeAudioUploadJSON(w, r, logger, mediaID)

		return
	}

	dest := fmt.Sprintf("/admin/quizzes/%d", quizID) + "#audio"
	http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // dest is built from a server-side id.
}

// writeAudioUploadError maps a StoreAudio error to an HTTP response: the audio
// caller-fault sentinels become 400 with a short message, an already-cancelled
// context surfaces as 499 with no log, anything else is logged and returned 500.
func writeAudioUploadError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, media.ErrAudioTooLarge):
		http.Error(w, "audio exceeds the maximum upload size", http.StatusBadRequest)
	case errors.Is(err, media.ErrEmptyUpload):
		http.Error(w, "audio file is empty", http.StatusBadRequest)
	case errors.Is(err, media.ErrUnsupportedAudio):
		http.Error(w, "unsupported audio format (use mp3, m4a, ogg, or wav)", http.StatusBadRequest)
	case errors.Is(err, context.Canceled):
		w.WriteHeader(httpStatusClientClosedRequest)
	default:
		logger.ErrorContext(r.Context(), "error storing uploaded audio", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)
	}
}

// writeAudioUploadJSON emits the stored row's id as JSON. Encoding to a buffer
// keeps an encoder failure from committing a truncated 200.
func writeAudioUploadJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, mediaID int64) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(audioUploadResultJSON{ID: mediaID}); err != nil {
		logger.ErrorContext(r.Context(), "error encoding audio upload response", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := w.Write(buf.Bytes()); err != nil {
		logger.ErrorContext(r.Context(), "error writing audio upload response", slog.Any("err", err))
	}
}
