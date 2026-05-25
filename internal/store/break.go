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

// ListBreaksByQuiz returns the breaks for a quiz in ascending position
// order. Used by the admin quiz view to render breaks interleaved with
// questions (#167).
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

// CreateBreak inserts a break at the caller-supplied position. Position
// is the "after question N" slot in the play sequence (0 = before the
// first question, N = after the question whose position is N). The
// UNIQUE INDEX breaks_quiz_position_idx surfaces a slot collision as
// [quiz.ErrBreakPositionTaken] so the admin form can render an inline
// error instead of a generic 500 (#167).
func (s *QuizStore) CreateBreak(ctx context.Context, b *quiz.Break) error {
	row, err := s.q.CreateBreak(ctx, db.CreateBreakParams{
		QuizID:   b.QuizID,
		Text:     b.Text,
		Position: int64(b.Position),
	})
	if err != nil {
		if isBreakUniqueViolation(err) {
			return quiz.ErrBreakPositionTaken
		}

		return fmt.Errorf("failed to create break: %w", err)
	}
	b.ID = row.ID
	b.CreatedAt = row.CreatedAt
	b.UpdatedAt = row.UpdatedAt

	return nil
}

// UpdateBreak updates an existing break's mutable fields (text +
// position). A position change that collides with another break on the
// same quiz surfaces as [quiz.ErrBreakPositionTaken]; a stale id maps
// to [quiz.ErrUpdatingBreakNoRowsAffected] (#167).
func (s *QuizStore) UpdateBreak(ctx context.Context, b *quiz.Break) error {
	if b.ID == 0 {
		return quiz.ErrCannotUpdateBreakWithIDZero
	}

	res, err := s.q.UpdateBreak(ctx, db.UpdateBreakParams{
		Text:     b.Text,
		Position: int64(b.Position),
		ID:       b.ID,
	})
	if err != nil {
		if isBreakUniqueViolation(err) {
			return quiz.ErrBreakPositionTaken
		}

		return fmt.Errorf("failed to update break: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingBreakNoRowsAffected
	}

	return nil
}

// isBreakUniqueViolation reports whether err is the SQLite
// SQLITE_CONSTRAINT_UNIQUE that breaks_quiz_position_idx raises when a
// (quiz_id, position) slot is already in use.
func isBreakUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error

	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
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
