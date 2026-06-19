package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/media"
)

// MediaStore wraps the generated media queries and maps rows to the
// media.Media domain type. It satisfies media.Store.
type MediaStore struct {
	q      *db.Queries
	logger *slog.Logger
}

// NewMediaStore initializes a new MediaStore with the provided database connection.
func NewMediaStore(conn *sql.DB, logger *slog.Logger) *MediaStore {
	return &MediaStore{q: db.New(conn), logger: logger}
}

// CreateMedia inserts a media row not-ready and returns it with the assigned id
// and created_at populated. Width and Height of 0 store NULL (a non-image type
// later may have no pixel dimensions); an empty ThumbPath stores NULL.
func (s *MediaStore) CreateMedia(ctx context.Context, m *media.Media) (*media.Media, error) {
	row, err := s.q.CreateMedia(ctx, db.CreateMediaParams{
		QuizID:            m.QuizID,
		Type:              m.Type,
		Mime:              m.MIME,
		Path:              m.Path,
		ThumbPath:         sql.NullString{String: m.ThumbPath, Valid: m.ThumbPath != ""},
		Width:             sql.NullInt64{Int64: int64(m.Width), Valid: m.Width != 0},
		Height:            sql.NullInt64{Int64: int64(m.Height), Valid: m.Height != 0},
		SizeBytes:         m.SizeBytes,
		Sha256:            m.SHA256,
		DurationMs:        nullableInt(m.DurationMs),
		CreatedByPlayerID: m.CreatedByPlayerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create media: %w", err)
	}

	return mediaFromRow(row), nil
}

// UpdateMediaPaths sets the on-disk paths of a media row once its files are
// written. Returns media.ErrMediaNotFound when no row matched.
func (s *MediaStore) UpdateMediaPaths(ctx context.Context, id int64, path, thumbPath string) error {
	res, err := s.q.UpdateMediaPaths(ctx, db.UpdateMediaPathsParams{
		Path:      path,
		ThumbPath: sql.NullString{String: thumbPath, Valid: thumbPath != ""},
		ID:        id,
	})
	if err != nil {
		return fmt.Errorf("failed to update media paths: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read media paths update result: %w", err)
	}
	if affected == 0 {
		return media.ErrMediaNotFound
	}

	return nil
}

// MarkMediaReady flips a media row ready, the final step of the two-phase
// upload. Returns media.ErrMediaNotFound when no row matched.
func (s *MediaStore) MarkMediaReady(ctx context.Context, id int64) error {
	res, err := s.q.MarkMediaReady(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to mark media ready: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read media ready update result: %w", err)
	}
	if affected == 0 {
		return media.ErrMediaNotFound
	}

	return nil
}

// ListStaleNotReadyMedia returns not-ready media rows older than olderThan. The
// window is passed to SQL as whole seconds (the cutoff date is computed there),
// so a sub-second olderThan rounds down to its second floor.
func (s *MediaStore) ListStaleNotReadyMedia(
	ctx context.Context, olderThan time.Duration,
) ([]media.StaleMedia, error) {
	rows, err := s.q.ListStaleNotReadyMedia(ctx, int64(olderThan.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("failed to list stale not-ready media: %w", err)
	}

	items := make([]media.StaleMedia, 0, len(rows))
	for _, row := range rows {
		items = append(items, media.StaleMedia{
			ID:        row.ID,
			Path:      row.Path,
			ThumbPath: row.ThumbPath.String,
		})
	}

	return items, nil
}

// GetMedia returns the media row for id, or media.ErrMediaNotFound when no row
// matches.
func (s *MediaStore) GetMedia(ctx context.Context, id int64) (*media.Media, error) {
	row, err := s.q.GetMedia(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, media.ErrMediaNotFound
		}

		return nil, fmt.Errorf("failed to get media: %w", err)
	}

	return mediaFromRow(row), nil
}

// ListMediaByQuiz returns every ready media row for quizID, newest first.
func (s *MediaStore) ListMediaByQuiz(ctx context.Context, quizID int64) ([]*media.Media, error) {
	rows, err := s.q.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list media by quiz: %w", err)
	}

	items := make([]*media.Media, 0, len(rows))
	for _, row := range rows {
		items = append(items, mediaFromRow(row))
	}

	return items, nil
}

// DeleteMedia removes the media row for id. Returns media.ErrMediaNotFound when
// no row matched so the caller can distinguish a real delete from a no-op.
func (s *MediaStore) DeleteMedia(ctx context.Context, id int64) error {
	res, err := s.q.DeleteMedia(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete media: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read media delete result: %w", err)
	}
	if affected == 0 {
		return media.ErrMediaNotFound
	}

	return nil
}

// CountMediaByQuizAndType returns the number of ready media rows of mediaType
// for quizID, so each upload route enforces a per-type library ceiling.
func (s *MediaStore) CountMediaByQuizAndType(ctx context.Context, quizID int64, mediaType string) (int64, error) {
	n, err := s.q.CountMediaByQuizAndType(ctx, db.CountMediaByQuizAndTypeParams{
		QuizID: quizID,
		Type:   mediaType,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count media by quiz and type: %w", err)
	}

	return n, nil
}

// mediaFromRow maps a generated db.Medium row to the media.Media domain type.
// A NULL thumb_path / width / height surfaces as the Go zero value; a NULL
// duration_ms surfaces as a nil *int so "unknown" stays distinct from zero.
func mediaFromRow(row db.Medium) *media.Media {
	return &media.Media{
		ID:                row.ID,
		QuizID:            row.QuizID,
		Type:              row.Type,
		MIME:              row.Mime,
		Path:              row.Path,
		ThumbPath:         row.ThumbPath.String,
		Width:             int(row.Width.Int64),
		Height:            int(row.Height.Int64),
		SizeBytes:         row.SizeBytes,
		SHA256:            row.Sha256,
		DurationMs:        nullableIntToPtr(row.DurationMs),
		CreatedByPlayerID: row.CreatedByPlayerID,
		CreatedAt:         row.CreatedAt,
	}
}
