package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

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

// MoveBreak shifts a break by one slot in the given direction within
// its owning quiz. "up" decrements the position, "down" increments it.
// The target slot must be a valid play-sequence slot - either 0 (the
// before-first-question slot) or the position of one of the quiz's
// questions - and must not already be occupied by another break.
//
// Returns:
//   - [quiz.ErrInvalidDirection] when direction is neither "up" nor
//     "down".
//   - [quiz.ErrBreakNotFound] when breakID does not belong to quizID.
//   - [quiz.ErrBreakMoveImpossible] when the resulting slot is out of
//     range or already occupied.
//
// The eligibility check and the position update happen against the
// same snapshot via [database.ExecTx] so a concurrent break create
// cannot squeeze into the target slot between the check and the move.
func (s *QuizStore) MoveBreak(ctx context.Context, quizID, breakID int64, direction string) error {
	if direction != quiz.DirectionUp && direction != quiz.DirectionDown {
		return quiz.ErrInvalidDirection
	}

	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return moveBreakTx(ctx, q, quizID, breakID, direction)
	}); err != nil {
		return fmt.Errorf("failed to move break: %w", err)
	}

	return nil
}

// moveBreakTx is the body of MoveBreak's transactional path. Pulled
// out so the public method stays under revive's cognitive-complexity
// budget; structuring it as a closure for ExecTx would have wrapped
// the sentinel errors in [fmt.Errorf] and required [errors.Is]
// unwrapping at the call site.
func moveBreakTx(
	ctx context.Context, q *db.Queries, quizID, breakID int64, direction string,
) error {
	current, err := q.GetBreak(ctx, breakID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quiz.ErrBreakNotFound
		}

		return fmt.Errorf("failed to load break for move: %w", err)
	}
	if current.QuizID != quizID {
		return quiz.ErrBreakNotFound
	}

	var newPos int64
	switch direction {
	case quiz.DirectionUp:
		newPos = current.Position - 1
	case quiz.DirectionDown:
		newPos = current.Position + 1
	default:
		// Guarded above by MoveBreak's early return for invalid
		// directions, so this branch is unreachable. The explicit
		// default keeps revive's enforce-switch-style happy and pins
		// the invariant in code for the next reader.
		return quiz.ErrInvalidDirection
	}

	validSlots, err := loadBreakSlots(ctx, q, quizID)
	if err != nil {
		return err
	}
	if !slices.Contains(validSlots, newPos) {
		return quiz.ErrBreakMoveImpossible
	}

	siblings, err := q.ListBreaksByQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list breaks for move: %w", err)
	}
	for _, b := range siblings {
		if b.ID == breakID {
			continue
		}
		if b.Position == newPos {
			return quiz.ErrBreakMoveImpossible
		}
	}

	if _, err := q.UpdateBreakPosition(ctx, db.UpdateBreakPositionParams{
		Position: newPos,
		ID:       breakID,
	}); err != nil {
		return fmt.Errorf("failed to update break position: %w", err)
	}

	return nil
}

// loadBreakSlots returns the set of positions a break is allowed to
// sit at on the given quiz: 0 (the before-first-question slot) plus
// every question's position. The result is ordered ascending so a
// future SearchInts-based check stays valid; callers today use
// [slices.Contains].
func loadBreakSlots(ctx context.Context, q *db.Queries, quizID int64) ([]int64, error) {
	questions, err := q.ListQuestionsByQuizID(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list questions for break slots: %w", err)
	}
	slots := make([]int64, 0, len(questions)+1)
	slots = append(slots, 0)
	for _, qst := range questions {
		slots = append(slots, qst.Position)
	}

	return slots, nil
}
