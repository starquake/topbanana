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

// UpdateRound updates an existing round's mutable fields (title, summary,
// position). A position change that collides with another round on
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

// DeleteRound removes a round and its questions by ID (#444). The round's
// questions would cascade via the ON DELETE CASCADE on questions.round_id,
// but game_questions.question_id and game_answers.option_id reference those
// questions without ON DELETE CASCADE, so a played round's bare delete trips
// FOREIGN KEY constraint failed (787). Run inside a transaction and clean up
// each question's dependent game_questions / game_answers rows via
// execDeleteQuestion before dropping the round (#788).
func (s *QuizStore) DeleteRound(ctx context.Context, id int64) error {
	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.deleteRoundTx(ctx, q, id)
	}); err != nil {
		return fmt.Errorf("failed to delete round: %w", err)
	}

	return nil
}

// deleteRoundTx is the transactional body of DeleteRound. Pulled out so the
// sentinel error is not wrapped on the way out.
func (s *QuizStore) deleteRoundTx(ctx context.Context, q *db.Queries, id int64) error {
	questionIDs, err := q.ListQuestionIDsByRoundID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to list question IDs for round %d: %w", id, err)
	}
	if err = s.execDeleteQuestions(ctx, q, questionIDs); err != nil {
		return err
	}

	res, err := q.DeleteRound(ctx, id)
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

// MoveRoundToPosition moves a round to an absolute 1-based slot within
// its quiz, renumbering every round so positions stay dense 1..N. The
// load, renumber, and writes share a transaction so a concurrent round
// create cannot squeeze into a slot mid-renumber (#199).
func (s *QuizStore) MoveRoundToPosition(ctx context.Context, quizID, roundID int64, newPosition int) error {
	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return moveRoundToPositionTx(ctx, q, quizID, roundID, newPosition)
	}); err != nil {
		return fmt.Errorf("failed to move round to position: %w", err)
	}

	return nil
}

// moveRoundToPositionTx is the transactional body of
// MoveRoundToPosition. Pulled out so the public method stays under
// revive's cognitive-complexity budget and the sentinel errors are not
// wrapped on the way out.
func moveRoundToPositionTx(ctx context.Context, q *db.Queries, quizID, roundID int64, newPosition int) error {
	rounds, err := q.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list rounds for move to position: %w", err)
	}

	idx := slices.IndexFunc(rounds, func(g db.Round) bool { return g.ID == roundID })
	if idx == -1 {
		return quiz.ErrRoundNotFound
	}

	moved := rounds[idx]
	reordered := slices.Delete(slices.Clone(rounds), idx, idx+1)
	target := clampIndex(newPosition, len(reordered))
	reordered = slices.Insert(reordered, target, moved)

	ids := make([]int64, len(reordered))
	current := make([]int64, len(reordered))
	for i, rnd := range reordered {
		ids[i], current[i] = rnd.ID, rnd.Position
	}

	return renumberDensePositions(ctx, ids, current, func(ctx context.Context, id, pos int64) error {
		if _, err := q.UpdateRoundPosition(ctx, db.UpdateRoundPositionParams{Position: pos, ID: id}); err != nil {
			return fmt.Errorf("update round position: %w", err)
		}

		return nil
	})
}

// renumberDensePositions assigns the ids the dense positions 1..N (in the
// order given) via setPos, using the negative-parking idiom. SQLite checks
// the UNIQUE(quiz_id, position) index per statement (no deferred
// uniqueness), so any row whose final position differs from its current
// one (current[i]) is first parked at a distinct negative slot (-id,
// unique because ids are positive) before the second pass assigns the
// final positions. Rows already at their final position are skipped so a
// no-op move writes nothing. Shared by the round and question reorder
// paths, which differ only in which Update*Position query setPos calls.
func renumberDensePositions(
	ctx context.Context,
	ids, current []int64,
	setPos func(ctx context.Context, id, pos int64) error,
) error {
	type move struct {
		id       int64
		finalPos int64
	}
	var moves []move
	for i, id := range ids {
		finalPos := int64(i + 1)
		if current[i] == finalPos {
			continue
		}
		moves = append(moves, move{id: id, finalPos: finalPos})
	}

	for _, m := range moves {
		if err := setPos(ctx, m.id, -m.id); err != nil {
			return fmt.Errorf("park position: %w", err)
		}
	}
	for _, m := range moves {
		if err := setPos(ctx, m.id, m.finalPos); err != nil {
			return fmt.Errorf("settle position: %w", err)
		}
	}

	return nil
}

// clampIndex maps a 1-based target position onto a valid insertion index
// in [0, length]. Out-of-range inputs clamp to the ends instead of
// erroring, matching the drag UX where a drop past either edge means
// "first" or "last".
func clampIndex(position, length int) int {
	idx := position - 1
	if idx < 0 {
		return 0
	}
	if idx > length {
		return length
	}

	return idx
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
