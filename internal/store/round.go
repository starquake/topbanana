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

// defaultRoundTitle is the title stamped on the single round every quiz
// starts with (#444). Matches the 'Round 1' backfill in migration
// 20260530000000 so a quiz created after the migration is
// indistinguishable from one migrated by it.
const defaultRoundTitle = "Round 1"

// ListRoundsByQuiz returns the question rounds for a quiz in ascending
// position order (#444).
func (s *QuizStore) ListRoundsByQuiz(ctx context.Context, quizID int64) ([]*quiz.Round, error) {
	rows, err := s.q.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list rounds for quiz %d: %w", quizID, err)
	}

	rounds := make([]*quiz.Round, 0, len(rows))
	for _, r := range rows {
		rounds = append(rounds, roundFromRow(r))
	}

	return rounds, nil
}

// GetRound returns a question round by its ID. Returns
// [quiz.ErrRoundNotFound] when no row matches.
func (s *QuizStore) GetRound(ctx context.Context, id int64) (*quiz.Round, error) {
	row, err := s.q.GetRound(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrRoundNotFound
		}

		return nil, fmt.Errorf("failed to get round: %w", err)
	}

	return roundFromRow(row), nil
}

// GetDefaultRound returns the lowest-position round for a quiz. Returns
// [quiz.ErrRoundNotFound] when the quiz has no rounds (#444).
func (s *QuizStore) GetDefaultRound(ctx context.Context, quizID int64) (*quiz.Round, error) {
	row, err := s.q.GetDefaultRound(ctx, quizID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrRoundNotFound
		}

		return nil, fmt.Errorf("failed to get default round for quiz %d: %w", quizID, err)
	}

	return roundFromRow(row), nil
}

// CreateRound inserts a question round at the caller-supplied position.
// The UNIQUE INDEX rounds_quiz_position_idx surfaces a slot
// collision as [quiz.ErrRoundPositionTaken] (#444).
func (s *QuizStore) CreateRound(ctx context.Context, g *quiz.Round) error {
	row, err := s.q.CreateRound(ctx, db.CreateRoundParams{
		QuizID:   g.QuizID,
		Position: int64(g.Position),
		Title:    g.Title,
		Summary:  g.Summary,
	})
	if err != nil {
		if isRoundUniqueViolation(err) {
			return quiz.ErrRoundPositionTaken
		}

		return fmt.Errorf("failed to create round: %w", err)
	}
	g.ID = row.ID
	g.CreatedAt = row.CreatedAt
	g.UpdatedAt = row.UpdatedAt

	return nil
}

// UpdateRound updates an existing round's mutable fields (title, break
// text, position). A position change that collides with another round on
// the same quiz surfaces as [quiz.ErrRoundPositionTaken]; a stale id maps
// to [quiz.ErrUpdatingRoundNoRowsAffected] (#444).
func (s *QuizStore) UpdateRound(ctx context.Context, g *quiz.Round) error {
	if g.ID == 0 {
		return quiz.ErrCannotUpdateRoundWithIDZero
	}

	res, err := s.q.UpdateRound(ctx, db.UpdateRoundParams{
		Title:    g.Title,
		Summary:  g.Summary,
		Position: int64(g.Position),
		ID:       g.ID,
	})
	if err != nil {
		if isRoundUniqueViolation(err) {
			return quiz.ErrRoundPositionTaken
		}

		return fmt.Errorf("failed to update round: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingRoundNoRowsAffected
	}

	return nil
}

// DeleteRound removes a round by ID (#444). The round's questions cascade
// via the ON DELETE CASCADE on questions.round_id.
func (s *QuizStore) DeleteRound(ctx context.Context, id int64) error {
	res, err := s.q.DeleteRound(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete round: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingRoundNoRowsAffected
	}

	return nil
}

// MoveQuestionToRound reassigns a question to a different round within
// the same quiz (#444). The lookups and the UPDATE share a transaction
// so a concurrent delete of either side cannot slip a dangling
// reference past the cross-quiz checks. Returns
// [quiz.ErrQuestionNotFound] when the question is missing or not on the
// quiz, and [quiz.ErrRoundNotFound] when the round is missing or not on
// the quiz.
func (s *QuizStore) MoveQuestionToRound(ctx context.Context, quizID, questionID, roundID int64) error {
	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return moveQuestionToRoundTx(ctx, q, quizID, questionID, roundID)
	}); err != nil {
		return fmt.Errorf("failed to move question to round: %w", err)
	}

	return nil
}

// moveQuestionToRoundTx is the transactional body of
// MoveQuestionToRound. Pulled out so the public method stays under
// revive's cognitive-complexity budget and the sentinel errors are not
// wrapped on the way out.
func moveQuestionToRoundTx(ctx context.Context, q *db.Queries, quizID, questionID, roundID int64) error {
	question, err := q.GetQuestion(ctx, questionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quiz.ErrQuestionNotFound
		}

		return fmt.Errorf("load question for round move: %w", err)
	}
	if question.QuizID != quizID {
		return quiz.ErrQuestionNotFound
	}

	round, err := q.GetRound(ctx, roundID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quiz.ErrRoundNotFound
		}

		return fmt.Errorf("load round for question move: %w", err)
	}
	if round.QuizID != quizID {
		return quiz.ErrRoundNotFound
	}

	if _, err := q.MoveQuestionToRound(ctx, db.MoveQuestionToRoundParams{
		RoundID: roundID,
		ID:      questionID,
	}); err != nil {
		return fmt.Errorf("update question round: %w", err)
	}

	return nil
}

// MoveRound shifts a round by one slot within its quiz. The eligibility
// check and the UPDATE share a transaction so a concurrent round create
// cannot squeeze into the target slot between them (#444).
func (s *QuizStore) MoveRound(ctx context.Context, quizID, roundID int64, direction string) error {
	if direction != quiz.DirectionUp && direction != quiz.DirectionDown {
		return quiz.ErrInvalidDirection
	}

	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return moveRoundTx(ctx, q, quizID, roundID, direction)
	}); err != nil {
		return fmt.Errorf("failed to move round: %w", err)
	}

	return nil
}

// moveRoundTx is the transactional body of MoveRound. Pulled out so the
// public method stays under revive's cognitive-complexity budget;
// structuring it as a closure for ExecTx would have wrapped the sentinel
// errors in [fmt.Errorf] and required [errors.Is] unwrapping at the call
// site.
func moveRoundTx(
	ctx context.Context, q *db.Queries, quizID, roundID int64, direction string,
) error {
	siblings, err := q.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list rounds for move: %w", err)
	}

	idx := slices.IndexFunc(siblings, func(g db.Round) bool { return g.ID == roundID })
	if idx == -1 {
		return quiz.ErrRoundNotFound
	}

	var neighbourIdx int
	switch direction {
	case quiz.DirectionUp:
		if idx == 0 {
			return quiz.ErrRoundMoveImpossible
		}
		neighbourIdx = idx - 1
	case quiz.DirectionDown:
		if idx == len(siblings)-1 {
			return quiz.ErrRoundMoveImpossible
		}
		neighbourIdx = idx + 1
	default:
		// Guarded by MoveRound's early return for invalid directions, so
		// this branch is unreachable. The explicit default keeps revive's
		// enforce-switch-style happy.
		return quiz.ErrInvalidDirection
	}

	return swapRoundPositions(ctx, q, siblings[idx], siblings[neighbourIdx])
}

// swapRoundPositions swaps the positions of two rounds using a parking
// slot. SQLite checks UNIQUE(quiz_id, position) per statement, so a naive
// two-step swap trips the constraint on the first UPDATE. Park the
// current round at -current.ID - guaranteed unique because IDs are
// positive - move the neighbour into the current slot, then settle the
// current round into the neighbour's old slot.
func swapRoundPositions(ctx context.Context, q *db.Queries, current, neighbour db.Round) error {
	if _, err := q.UpdateRoundPosition(ctx, db.UpdateRoundPositionParams{
		Position: -current.ID,
		ID:       current.ID,
	}); err != nil {
		return fmt.Errorf("park current round position: %w", err)
	}
	if _, err := q.UpdateRoundPosition(ctx, db.UpdateRoundPositionParams{
		Position: current.Position,
		ID:       neighbour.ID,
	}); err != nil {
		return fmt.Errorf("update neighbour round position: %w", err)
	}
	if _, err := q.UpdateRoundPosition(ctx, db.UpdateRoundPositionParams{
		Position: neighbour.Position,
		ID:       current.ID,
	}); err != nil {
		return fmt.Errorf("update current round position: %w", err)
	}

	return nil
}

// roundFromRow maps a sqlc rounds row onto the domain type.
func roundFromRow(r db.Round) *quiz.Round {
	return &quiz.Round{
		ID:        r.ID,
		QuizID:    r.QuizID,
		Position:  int(r.Position),
		Title:     r.Title,
		Summary:   r.Summary,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// isRoundUniqueViolation reports whether err is the SQLite
// SQLITE_CONSTRAINT_UNIQUE that rounds_quiz_position_idx raises
// when a (quiz_id, position) slot is already in use.
func isRoundUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error

	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
