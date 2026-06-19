package media

import (
	"context"
	"errors"
	"time"
)

// TypeImage is the media.type value for an image row. The column is
// type-discriminated (image|video|audio) so the same table and per-quiz
// directory carry audio and video without a schema change.
const TypeImage = "image"

// TypeAudio is the media.type value for an audio row (#1059). Audio is stored
// as-is (no server-side transcoding); only already-browser-playable formats are
// accepted.
const TypeAudio = "audio"

// ErrMediaNotFound is returned when a media id does not name a row.
var ErrMediaNotFound = errors.New("media not found")

// ErrPathEscapesRoot is returned when a stored media path would resolve outside
// the configured media root (a traversal via ".."). It guards the file-open
// path against a corrupt or hostile DB value.
var ErrPathEscapesRoot = errors.New("media path escapes root")

// Media is a single stored media item scoped to a quiz. Path and ThumbPath are
// filesystem paths relative to the configured media root; the serving layer
// resolves them against that root rather than trusting an absolute path from
// the DB.
type Media struct {
	ID        int64
	QuizID    int64
	Type      string
	MIME      string
	Path      string
	ThumbPath string
	Width     int
	Height    int
	SizeBytes int64
	SHA256    string
	// DurationMs is the playback length of an audio row in milliseconds, or nil
	// when unknown (#1059). It is advisory and supplied by the caller, since
	// audio is not decoded server-side; an image row leaves it nil.
	DurationMs        *int
	CreatedByPlayerID int64
	CreatedAt         time.Time
}

// StaleMedia is a not-ready media row the sweep is about to drop: its id plus
// the file paths to unlink before the row is deleted. ThumbPath is empty when
// the row never reached the path-recording step.
type StaleMedia struct {
	ID        int64
	Path      string
	ThumbPath string
}

// Store is the persistence surface the media Service needs. It is defined here
// (the consumer) so the domain does not import the concrete store package; the
// concrete *store.MediaStore satisfies it.
type Store interface {
	// CreateMedia inserts a media row not-ready and returns it with its
	// assigned id and created_at populated. The row's Path and ThumbPath may be
	// empty at insert time; they are filled in via UpdateMediaPaths once the
	// assigned id names the on-disk files. MarkMediaReady flips it ready once
	// the files are written and the paths recorded. DurationMs is persisted as
	// supplied (nil stores NULL) for audio rows.
	CreateMedia(ctx context.Context, m *Media) (*Media, error)
	// UpdateMediaPaths sets the on-disk paths of a media row after its files are
	// written. Returns ErrMediaNotFound when no row matched.
	UpdateMediaPaths(ctx context.Context, id int64, path, thumbPath string) error
	// MarkMediaReady flips a media row ready, the final step of the two-phase
	// upload. Returns ErrMediaNotFound when no row matched.
	MarkMediaReady(ctx context.Context, id int64) error
	// GetMedia returns the media row for id, or ErrMediaNotFound.
	GetMedia(ctx context.Context, id int64) (*Media, error)
	// ListMediaByQuiz returns every ready media row for quizID, newest first.
	ListMediaByQuiz(ctx context.Context, quizID int64) ([]*Media, error)
	// ListStaleNotReadyMedia returns not-ready rows older than olderThan, the
	// candidates the in-flight-upload sweep removes.
	ListStaleNotReadyMedia(ctx context.Context, olderThan time.Duration) ([]StaleMedia, error)
	// DeleteMedia removes the media row for id. Returns ErrMediaNotFound when
	// no row matched.
	DeleteMedia(ctx context.Context, id int64) error
	// CountMediaByQuiz returns the number of ready media rows for quizID.
	CountMediaByQuiz(ctx context.Context, quizID int64) (int64, error)
}
