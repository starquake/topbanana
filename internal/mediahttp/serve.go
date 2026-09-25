package mediahttp

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

// publicCacheControl and privateCacheControl key the cache policy to whether
// the owning quiz is published and public. Both rely on the stored sha256 ETag
// for correctness; we dropped `immutable` from the public policy in #951 (id
// reuse across delete + concurrent upload could briefly point a stable id at
// different bytes), but the AUTOINCREMENT migration in the same PR now
// guarantees ids are never reused. A short max-age + must-revalidate gives the
// browser and any intermediate cache a small free hit window while still
// revalidating against the ETag once the window expires, so a corrected image
// propagates within the TTL.
const (
	publicCacheControl  = "public, max-age=300, must-revalidate"
	privateCacheControl = "private, no-cache"
)

// HandleMediaServe serves the full jpeg for GET /media/{id}. Authorization
// mirrors the owning quiz's own access rule (see authorizeMediaRead). The id is
// an integer path param; the file is streamed via [http.ServeContent] so ETag /
// If-None-Match / range handling come for free.
func HandleMediaServe(
	logger *slog.Logger, svc MediaService, quizzes QuizMetaLookup, viewer Viewer,
) http.Handler {
	return serveMedia(logger, svc, quizzes, viewer, fullPath)
}

// HandleMediaThumb serves the 480px jpeg thumbnail for GET /media/{id}/thumb.
// Same authorization and caching as HandleMediaServe; it differs only in which
// of the row's two files it streams.
func HandleMediaThumb(
	logger *slog.Logger, svc MediaService, quizzes QuizMetaLookup, viewer Viewer,
) http.Handler {
	return serveMedia(logger, svc, quizzes, viewer, thumbPath)
}

// fullPath and thumbPath select which of a row's two files a serving handler
// streams. Passing the selector (rather than a bool flag) keeps serveMedia free
// of a control-flow flag parameter.
func fullPath(m *media.Media) string  { return m.Path }
func thumbPath(m *media.Media) string { return m.ThumbPath }

// serveMedia is the shared body of the full / thumb serving handlers. pickPath
// chooses the file to stream. It loads the row, gates on the owning quiz, then
// streams the chosen file with the matching Cache-Control and the sha256 ETag.
func serveMedia(
	logger *slog.Logger, svc MediaService, quizzes QuizMetaLookup,
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

		cacheControl, ok := authorizeMediaRead(w, r, logger, quizzes, viewer, m.QuizID)
		if !ok {
			return
		}

		relPath := pickPath(m)
		if relPath == "" {
			http.NotFound(w, r)

			return
		}

		streamMedia(w, r, logger, svc, m, relPath, cacheControl)
	})
}

// authorizeMediaRead applies the owning quiz's read rule (mediaReadable) and
// returns the Cache-Control to serve with; every refusal is an opaque 404.
func authorizeMediaRead(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, quizzes QuizMetaLookup, viewer Viewer, quizID int64,
) (string, bool) {
	qz, err := quizzes.GetQuizMeta(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return "", false
		}
		logger.ErrorContext(r.Context(), "error loading quiz for media gate", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return "", false
	}

	p, signedIn := viewer(r)
	if !signedIn {
		p = nil
	}
	if !mediaReadable(qz, p) {
		http.NotFound(w, r)

		return "", false
	}

	if qz.Published && qz.Visibility == quiz.VisibilityPublic {
		return publicCacheControl, true
	}

	return privateCacheControl, true
}

// mediaReadable reports whether p (nil when not signed in) may read qz's media.
// The creator and admins always may, so the editor and preview keep working; a
// draft live quiz stays readable because hosts can run one (#1207).
func mediaReadable(qz *quiz.Quiz, p *auth.Player) bool {
	if p != nil && (p.IsAdmin() || p.ID == qz.CreatedByPlayerID) {
		return true
	}
	if !qz.Published && qz.Mode != quiz.ModeLive {
		return false
	}

	return qz.Visibility != quiz.VisibilityPrivate || p != nil
}

// streamMedia opens and streams the chosen file via [http.ServeContent], which
// handles the ETag / If-None-Match / Range dance. The Content-Type is the row's
// stored mime, the ETag is the stored image's sha256 (quoted, a strong
// validator), and Cache-Control is the policy authorizeMediaRead chose.
func streamMedia(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, svc MediaService, m *media.Media, relPath, cacheControl string,
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
	w.Header().Set("Cache-Control", cacheControl)

	// ServeContent reads the ETag from the header we set and answers a matching
	// If-None-Match with 304, so the conditional-request handling is not
	// reimplemented here. The name is only used for content-type sniffing, which
	// our explicit Content-Type pre-empts; created_at is the modtime.
	http.ServeContent(w, r, m.Path, m.CreatedAt, f)
}
