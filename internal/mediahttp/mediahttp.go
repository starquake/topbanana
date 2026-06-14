// Package mediahttp holds the HTTP handlers for the media slice (#936): the
// host/admin upload endpoint and the public-entry serving endpoints. The image
// pipeline, store, and filesystem persistence live in internal/media; this
// package is only the HTTP edge - request parsing, authorization, and
// streaming - so internal/media stays free of net/http.
package mediahttp

import (
	"context"
	"io"
	"net/http"
	"os"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/media"
)

// Viewer resolves the authenticated (registered, non-anonymous) player from the
// request session, or (nil, false) when the request is anonymous or cookieless.
// The serving handlers use it for the private-quiz gate WITHOUT minting a player
// row or setting a session cookie: a media response is cacheable (public images
// carry Cache-Control: public, immutable), so it must not attach a Set-Cookie or
// create a row the way EnsurePlayer would - the same reason the static asset
// routes are not EnsurePlayer-wrapped.
type Viewer func(r *http.Request) (*auth.Player, bool)

// MediaService is the slice of *media.Service the handlers use. Defined here
// (the consumer) so a test can substitute a fault-injection double, and so the
// HTTP layer depends on the narrow surface it calls rather than the whole
// service.
type MediaService interface {
	// Store processes an uploaded image through the pipeline, writes the webp
	// full + thumbnail under the quiz directory, records a row, and returns it.
	Store(ctx context.Context, quizID, createdBy int64, r io.Reader) (*media.Media, error)
	// Get returns the media row for id, or media.ErrMediaNotFound.
	Get(ctx context.Context, id int64) (*media.Media, error)
	// Open opens a stored media file for reading by its root-relative path. The
	// caller closes the returned file.
	Open(relPath string) (*os.File, error)
}

// QuizVisibilityLookup is the slice of the quiz store the serving handlers use
// to mirror the owning quiz's access rule onto its media.
type QuizVisibilityLookup interface {
	// GetQuizVisibility returns just the visibility of a quiz by ID. Returns
	// quiz.ErrQuizNotFound when the quiz does not exist.
	GetQuizVisibility(ctx context.Context, id int64) (string, error)
}
