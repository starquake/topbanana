package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/quiz"
)

// createBreakAtNextPositionRetries caps the optimistic retry loop in
// [QuizStore.CreateBreakAtNextPosition] so two genuinely concurrent
// callers eventually serialize without spinning forever. Three attempts
// covers the realistic admin-double-click case (one slot collision)
// while still giving up loudly if something stranger is going on.
// Mirrors createQuestionAtNextPositionRetries (#352).
const createBreakAtNextPositionRetries = 3

// ListBreaksByQuiz returns the breaks for a quiz in ascending position
// order. Used by the admin quiz view to render the Breaks section
// alongside Questions (#167).
func (s *QuizStore) ListBreaksByQuiz(ctx context.Context, quizID int64) ([]*quiz.Break, error) {
	rows, err := s.q.ListBreaksByQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list breaks for quiz %d: %w", quizID, err)
	}

	breaks := make([]*quiz.Break, 0, len(rows))
	for _, r := range rows {
		breaks = append(breaks, &quiz.Break{
			ID:        r.ID,
			QuizID:    r.QuizID,
			Position:  int(r.Position),
			Text:      r.Text,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	}

	return breaks, nil
}

// GetBreak returns a break by its ID. Returns
// [quiz.ErrBreakNotFound] when no row matches.
func (s *QuizStore) GetBreak(ctx context.Context, id int64) (*quiz.Break, error) {
	row, err := s.q.GetBreak(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrBreakNotFound
		}

		return nil, fmt.Errorf("failed to get break: %w", err)
	}

	return &quiz.Break{
		ID:        row.ID,
		QuizID:    row.QuizID,
		Position:  int(row.Position),
		Text:      row.Text,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// CreateBreakAtNextPosition reads max(position)+1 and inserts the
// break with that position inside a single transaction. The UNIQUE
// INDEX on breaks(quiz_id, position) catches the race when two
// transactions both pick the same slot; this method retries up to
// [createBreakAtNextPositionRetries] times so genuinely concurrent
// admin clicks succeed without surfacing the constraint to the
// caller. Mirrors [QuizStore.CreateQuestionAtNextPosition].
func (s *QuizStore) CreateBreakAtNextPosition(ctx context.Context, b *quiz.Break) error {
	var lastErr error
	for range createBreakAtNextPositionRetries {
		txErr := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
			maxPos, err := q.NextBreakPosition(ctx, b.QuizID)
			if err != nil {
				return fmt.Errorf("read max break position: %w", err)
			}
			b.Position = int(maxPos) + 1

			row, err := q.CreateBreak(ctx, db.CreateBreakParams{
				QuizID:   b.QuizID,
				Text:     b.Text,
				Position: int64(b.Position),
			})
			if err != nil {
				return fmt.Errorf("insert break: %w", err)
			}
			b.ID = row.ID
			b.CreatedAt = row.CreatedAt
			b.UpdatedAt = row.UpdatedAt

			return nil
		})
		if txErr == nil {
			return nil
		}
		var sqliteErr *sqlite.Error
		if errors.As(txErr, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			lastErr = txErr

			continue
		}

		return fmt.Errorf("failed to create break at next position: %w", txErr)
	}

	return fmt.Errorf(
		"failed to create break after %d position retries: %w",
		createBreakAtNextPositionRetries,
		lastErr,
	)
}

// UpdateBreak updates an existing break. Currently only Text is
// mutable from the admin form (#167); position is reordered out of
// band in a future slice.
func (s *QuizStore) UpdateBreak(ctx context.Context, b *quiz.Break) error {
	if b.ID == 0 {
		return quiz.ErrCannotUpdateBreakWithIDZero
	}

	res, err := s.q.UpdateBreak(ctx, db.UpdateBreakParams{
		Text: b.Text,
		ID:   b.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update break: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingBreakNoRowsAffected
	}

	return nil
}

// DeleteBreak removes a break by ID. Breaks don't own dependent rows
// in slice 1, so the delete is a single statement.
func (s *QuizStore) DeleteBreak(ctx context.Context, id int64) error {
	res, err := s.q.DeleteBreak(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete break: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingBreakNoRowsAffected
	}

	return nil
}
