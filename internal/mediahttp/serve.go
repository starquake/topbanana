package mediahttp

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

// publicCacheControl and privateCacheControl key the cache policy to the owning
// quiz's visibility. Both revalidate via the stored sha256 ETag rather than
// trusting the id-as-immutable: a public quiz's image is shareable across
// caches, a private quiz's image must not be. We dropped `immutable` from the
// public policy in #951 - id reuse across delete + concurrent upload could
// briefly point a stable id at different bytes, and an immutable cache hid
// that mismatch from the host for the full week TTL.
const (
	publicCacheControl  = "public, no-cache"
	privateCacheControl = "private, no-cache"
)

// HandleMediaServe serves the full webp for GET /media/{id}. Authorization
// mirrors the owning quiz's own access rule: a public quiz's image is served to
// anyone (including anonymous players mid-game), a private quiz's image only to
// an authenticated viewer resolved by viewer (no player row is minted - the
// response is cacheable). The id is an integer path param; the file is streamed
// via [http.ServeContent] so ETag / If-None-Match / range handling come for free.
func HandleMediaServe(
	logger *slog.Logger, svc MediaService, quizzes QuizVisibilityLookup, viewer Viewer,
) http.Handler {
	return serveMedia(logger, svc, quizzes, viewer, fullPath)
}

// HandleMediaThumb serves the 480px webp thumbnail for GET /media/{id}/thumb.
// Same authorization and caching as HandleMediaServe; it differs only in which
// of the row's two files it streams.
func HandleMediaThumb(
	logger *slog.Logger, svc MediaService, quizzes QuizVisibilityLookup, viewer Viewer,
) http.Handler {
	return serveMedia(logger, svc, quizzes, viewer, thumbPath)
}

// fullPath and thumbPath select which of a row's two files a serving handler
// streams. Passing the selector (rather than a bool flag) keeps serveMedia free
// of a control-flow flag parameter.
func fullPath(m *media.Media) string  { return m.Path }
func thumbPath(m *media.Media) string { return m.ThumbPath }

// serveMedia is the shared body of the full / thumb serving handlers. pickPath
// chooses the file to stream. It loads the row, gates on the owning quiz's
// visibility, then streams the chosen file with a visibility-aware
// Cache-Control and the sha256 ETag.
func serveMedia(
	logger *slog.Logger, svc MediaService, quizzes QuizVisibilityLookup,
	viewer Viewer, pickPath func(*media.Media) string,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := handlers.ParseIDFromPath(w, r, logger, "id")
		if !ok {
			return
		}

		m, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, media.ErrMediaNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error loading media", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		visibility, ok := authorizeMediaRead(w, r, logger, quizzes, viewer, m.QuizID)
		if !ok {
			return
		}

		relPath := pickPath(m)
		if relPath == "" {
			http.NotFound(w, r)

			return
		}

		streamMedia(w, r, logger, svc, m, relPath, visibility)
	})
}

// authorizeMediaRead resolves the owning quiz's visibility and applies the
// play-surface read rule (mirrors clientapi.canReadQuiz): public/unlisted are
// reachable by anyone, private requires an authenticated (registered,
// non-anonymous) viewer. The viewer is resolved from the session without minting
// a player row, so a cacheable image response carries no Set-Cookie. A missing
// quiz or an unauthorized viewer is answered with a 404 so the gate is
// indistinguishable from a genuinely missing item, matching the play surface.
// Returns the visibility (for the cache policy) and whether to proceed.
func authorizeMediaRead(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, quizzes QuizVisibilityLookup, viewer Viewer, quizID int64,
) (string, bool) {
	visibility, err := quizzes.GetQuizVisibility(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return "", false
		}
		logger.ErrorContext(r.Context(), "error loading quiz visibility for media gate", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return "", false
	}

	if visibility == quiz.VisibilityPrivate {
		if _, ok := viewer(r); !ok {
			http.NotFound(w, r)

			return "", false
		}
	}

	return visibility, true
}

// streamMedia opens and streams the chosen file via [http.ServeContent], which
// handles the ETag / If-None-Match / Range dance. The Content-Type is the row's
// stored mime, the ETag is the stored image's sha256 (quoted, a strong
// validator), and Cache-Control is keyed to visibility.
func streamMedia(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, svc MediaService, m *media.Media, relPath, visibility string,
) {
	ctx := r.Context()
	f, err := svc.Open(relPath)
	if err != nil {
		if errors.Is(err, media.ErrPathEscapesRoot) {
			http.NotFound(w, r)

			return
		}
		logger.ErrorContext(ctx, "error opening media file", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			logger.ErrorContext(ctx, "error closing media file", slog.Any("err", cerr))
		}
	}()

	w.Header().Set("Content-Type", m.MIME)
	w.Header().Set("ETag", strconv.Quote(m.SHA256))
	if visibility == quiz.VisibilityPublic {
		w.Header().Set("Cache-Control", publicCacheControl)
	} else {
		w.Header().Set("Cache-Control", privateCacheControl)
	}

	// ServeContent reads the ETag from the header we set and answers a matching
	// If-None-Match with 304, so the conditional-request handling is not
	// reimplemented here. The name is only used for content-type sniffing, which
	// our explicit Content-Type pre-empts; created_at is the modtime.
	http.ServeContent(w, r, m.Path, m.CreatedAt, f)
}
