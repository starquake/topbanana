package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/quiz"
)

// QuizStore is a wrapper around database operations for managing quizzes and their related questions and options.
type QuizStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewQuizStore initializes a new QuizStore with the provided database connection and returns it.
func NewQuizStore(conn *sql.DB, logger *slog.Logger) *QuizStore {
	return &QuizStore{q: db.New(conn), db: conn, logger: logger}
}

// Ping checks the connection to the database, ensuring it's reachable and responsive.
func (s *QuizStore) Ping(ctx context.Context) error {
	err := s.db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// ListQuizzes returns a summary list of quizzes without questions or options.
// Includes rows of every visibility - use [QuizStore.ListPublicQuizzes] from
// public-facing handlers.
//
//nolint:dupl // Distinct sqlc row types, identical mapping; cannot share without reflection.
func (s *QuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	rows, err := s.q.ListQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list quizzes: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:                r.ID,
			Title:             r.Title,
			Slug:              r.Slug,
			Description:       r.Description,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
			CreatedByPlayerID: r.CreatedByPlayerID,
			TimeLimitSeconds:  int(r.TimeLimitSeconds),
			Visibility:        r.Visibility,
			Mode:              r.Mode,
			Language:          r.Language,
			PlayCount:         r.PlayCount,
			Published:         r.Published != 0,
			// INNER JOIN on players makes this a plain string (#359);
			// the FK guarantees a creator row exists.
			CreatedByDisplayName: r.CreatedByDisplayName,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// ListQuizzesForOwner returns the subset of [QuizStore.ListQuizzes] created by
// the given player (#1207). Same shape and ordering; used by the admin quiz
// list for a plain Host so they see only their own quizzes.
//
//nolint:dupl // See ListQuizzes: distinct sqlc row types, identical mapping.
func (s *QuizStore) ListQuizzesForOwner(ctx context.Context, ownerID int64) ([]*quiz.Quiz, error) {
	rows, err := s.q.ListQuizzesForOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list quizzes for owner: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:                r.ID,
			Title:             r.Title,
			Slug:              r.Slug,
			Description:       r.Description,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
			CreatedByPlayerID: r.CreatedByPlayerID,
			TimeLimitSeconds:  int(r.TimeLimitSeconds),
			Visibility:        r.Visibility,
			Mode:              r.Mode,
			Language:          r.Language,
			PlayCount:         r.PlayCount,
			Published:         r.Published != 0,
			// INNER JOIN, see ListQuizzes (#359).
			CreatedByDisplayName: r.CreatedByDisplayName,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// ListPublicQuizzes returns the visibility=public subset of
// [QuizStore.ListQuizzes] (#103). Same shape, same ordering - just the
// rows safe to surface to anonymous traffic.
//
//nolint:dupl // See ListQuizzes: distinct sqlc row types, identical mapping.
func (s *QuizStore) ListPublicQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	rows, err := s.q.ListPublicQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list public quizzes: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:                r.ID,
			Title:             r.Title,
			Slug:              r.Slug,
			Description:       r.Description,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
			CreatedByPlayerID: r.CreatedByPlayerID,
			TimeLimitSeconds:  int(r.TimeLimitSeconds),
			Visibility:        r.Visibility,
			Mode:              r.Mode,
			Language:          r.Language,
			PlayCount:         r.PlayCount,
			Published:         r.Published != 0,
			// INNER JOIN, see ListQuizzes (#359).
			CreatedByDisplayName: r.CreatedByDisplayName,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// ListLiveQuizzes returns the mode='live' subset of [QuizStore.ListQuizzes]
// (#836). Same shape, same ordering - just the rows a host can run live,
// which the intermission picker offers as the next quiz. Visibility is not
// filtered, matching CreateSession's host gate (mode='live' alone).
//
//nolint:dupl // See ListQuizzes: distinct sqlc row types, identical mapping.
func (s *QuizStore) ListLiveQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	rows, err := s.q.ListLiveQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list live quizzes: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:                r.ID,
			Title:             r.Title,
			Slug:              r.Slug,
			Description:       r.Description,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
			CreatedByPlayerID: r.CreatedByPlayerID,
			TimeLimitSeconds:  int(r.TimeLimitSeconds),
			Visibility:        r.Visibility,
			Mode:              r.Mode,
			Language:          r.Language,
			PlayCount:         r.PlayCount,
			Published:         r.Published != 0,
			// INNER JOIN, see ListQuizzes (#359).
			CreatedByDisplayName: r.CreatedByDisplayName,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// ListLiveQuizzesForOwner returns the subset of [QuizStore.ListLiveQuizzes]
// created by the given player (#1207). Same shape and ordering; used by the
// host picker for a plain Host so they see only their own live-eligible quizzes.
//
//nolint:dupl // See ListQuizzes: distinct sqlc row types, identical mapping.
func (s *QuizStore) ListLiveQuizzesForOwner(ctx context.Context, ownerID int64) ([]*quiz.Quiz, error) {
	rows, err := s.q.ListLiveQuizzesForOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list live quizzes for owner: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:                r.ID,
			Title:             r.Title,
			Slug:              r.Slug,
			Description:       r.Description,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
			CreatedByPlayerID: r.CreatedByPlayerID,
			TimeLimitSeconds:  int(r.TimeLimitSeconds),
			Visibility:        r.Visibility,
			Mode:              r.Mode,
			Language:          r.Language,
			PlayCount:         r.PlayCount,
			Published:         r.Published != 0,
			// INNER JOIN, see ListQuizzes (#359).
			CreatedByDisplayName: r.CreatedByDisplayName,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// QuestionCountsByQuiz returns the number of questions per quiz, keyed by
// quiz ID. Quizzes with zero questions are absent from the map; callers
// should treat a missing entry as 0. Pair with [QuizStore.ListQuizzes] when
// the list view needs to render counts without loading every quiz's full
// question + option tree.
func (s *QuizStore) QuestionCountsByQuiz(ctx context.Context) (map[int64]int, error) {
	rows, err := s.q.QuestionCountsByQuiz(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count questions by quiz: %w", err)
	}

	counts := make(map[int64]int, len(rows))
	for _, r := range rows {
		counts[r.QuizID] = int(r.QuestionCount)
	}

	return counts, nil
}

// QuizExists reports whether a quiz with the given ID exists. It runs a
// single one-row SELECT EXISTS probe and does not load the quiz's
// questions or options, so callers that only need to validate the quiz
// is real should prefer this over [QuizStore.GetQuiz].
func (s *QuizStore) QuizExists(ctx context.Context, id int64) (bool, error) {
	exists, err := s.q.QuizExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("failed to check quiz exists: %w", err)
	}

	return exists, nil
}

// GetQuiz returns a quiz by its ID.
func (s *QuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	row, err := s.q.GetQuiz(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrQuizNotFound
		}

		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	qz := &quiz.Quiz{
		ID:                row.ID,
		Title:             row.Title,
		Slug:              row.Slug,
		Description:       row.Description,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		CreatedByPlayerID: row.CreatedByPlayerID,
		TimeLimitSeconds:  int(row.TimeLimitSeconds),
		Visibility:        row.Visibility,
		Mode:              row.Mode,
		Language:          row.Language,
		PlayCount:         row.PlayCount,
		Published:         row.Published != 0,
		// INNER JOIN, see ListQuizzes (#359).
		CreatedByDisplayName: row.CreatedByDisplayName,
	}

	questions, err := s.ListQuestions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list questions for quiz %d: %w", id, err)
	}
	qz.Questions = questions

	return qz, nil
}

// GetQuizVisibility returns just the visibility of a quiz by its ID,
// without loading its questions or options. Returns ErrQuizNotFound when
// the quiz does not exist.
func (s *QuizStore) GetQuizVisibility(ctx context.Context, id int64) (string, error) {
	visibility, err := s.q.GetQuizVisibility(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", quiz.ErrQuizNotFound
		}

		return "", fmt.Errorf("failed to get quiz visibility: %w", err)
	}

	return visibility, nil
}

// CreateQuiz creates a new quiz using a transaction.
func (s *QuizStore) CreateQuiz(ctx context.Context, qz *quiz.Quiz) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execCreateQuiz(ctx, q, qz)
	})
	if err != nil {
		return fmt.Errorf("failed to create quiz: %w", err)
	}

	return nil
}

// UpdateQuiz updates a quiz using a transaction.
func (s *QuizStore) UpdateQuiz(ctx context.Context, qz *quiz.Quiz) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execUpdateQuiz(ctx, q, qz)
	})
	if err != nil {
		return fmt.Errorf("failed to update quiz: %w", err)
	}

	return nil
}

// SetQuizMode flips just the quiz's play mode (#830). It validates the mode
// up front so an invalid value never reaches the DB CHECK constraint, and
// maps a no-op update (id gone) to ErrQuizNotFound.
func (s *QuizStore) SetQuizMode(ctx context.Context, id int64, mode string) error {
	if !quiz.IsValidMode(mode) {
		return fmt.Errorf("%w: %q", quiz.ErrInvalidMode, mode)
	}

	res, err := s.q.UpdateQuizMode(ctx, db.UpdateQuizModeParams{Mode: mode, ID: id})
	if err != nil {
		return fmt.Errorf("failed to update quiz mode: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrQuizNotFound
	}

	return nil
}

// SetQuizPublished flips just the quiz's published flag (#1192), leaving its
// questions untouched, and maps a no-op update (id gone) to ErrQuizNotFound.
func (s *QuizStore) SetQuizPublished(ctx context.Context, id int64, published bool) error {
	res, err := s.q.SetQuizPublished(ctx, db.SetQuizPublishedParams{
		Published: boolToInt64(published),
		ID:        id,
	})
	if err != nil {
		return fmt.Errorf("failed to set quiz published: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrQuizNotFound
	}

	return nil
}

// UnpublishQuizIfUnplayed atomically returns a quiz to draft only while it has
// no real (non-preview) game (#1192). Reports whether a row was updated; false
// means the quiz is gone or has been played.
func (s *QuizStore) UnpublishQuizIfUnplayed(ctx context.Context, id int64) (bool, error) {
	res, err := s.q.UnpublishQuizIfUnplayed(ctx, id)
	if err != nil {
		return false, fmt.Errorf("failed to unpublish quiz if unplayed: %w", err)
	}

	return database.MustRowsAffected(res) > 0, nil
}

// QuizHasRealPlays reports whether the quiz has at least one non-preview game
// (#1192). Host preview games are excluded, so an owner previewing their draft
// never blocks a later unpublish.
func (s *QuizStore) QuizHasRealPlays(ctx context.Context, id int64) (bool, error) {
	hasPlays, err := s.q.QuizHasRealPlays(ctx, id)
	if err != nil {
		return false, fmt.Errorf("failed to check quiz real plays: %w", err)
	}

	return hasPlays, nil
}

// ListQuestions retrieves a list of questions for the specified quiz ID, including their options, from the data store.
func (s *QuizStore) ListQuestions(ctx context.Context, quizID int64) ([]*quiz.Question, error) {
	rows, err := s.q.ListQuestionsByQuizID(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list questions for quiz %d: %w", quizID, err)
	}

	optionsByQuestion, err := s.listOptionsByQuiz(ctx, quizID)
	if err != nil {
		return nil, err
	}

	questions := make([]*quiz.Question, 0, len(rows))
	for _, r := range rows {
		qs := &quiz.Question{
			ID:               r.ID,
			QuizID:           r.QuizID,
			RoundID:          r.RoundID,
			Text:             r.Text,
			Position:         int(r.Position),
			ImageMediaID:     nullableInt64ToPtr(r.ImageMediaID),
			AudioMediaID:     nullableInt64ToPtr(r.AudioMediaID),
			AudioRepeat:      r.AudioRepeat != 0,
			TimeLimitSeconds: nullableIntToPtr(r.TimeLimitSeconds),
		}

		options := optionsByQuestion[qs.ID]
		if options == nil {
			options = []*quiz.Option{}
		}
		qs.Options = options

		questions = append(questions, qs)
	}

	return questions, nil
}

// GetQuestion retrieves a question by its ID, including its options, from the data store or returns an appropriate error.
func (s *QuizStore) GetQuestion(ctx context.Context, id int64) (*quiz.Question, error) {
	row, err := s.q.GetQuestion(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrQuestionNotFound
		}

		return nil, fmt.Errorf("failed to get question: %w", err)
	}

	qs := &quiz.Question{
		ID:               row.ID,
		QuizID:           row.QuizID,
		RoundID:          row.RoundID,
		Text:             row.Text,
		Position:         int(row.Position),
		ImageMediaID:     nullableInt64ToPtr(row.ImageMediaID),
		AudioMediaID:     nullableInt64ToPtr(row.AudioMediaID),
		AudioRepeat:      row.AudioRepeat != 0,
		TimeLimitSeconds: nullableIntToPtr(row.TimeLimitSeconds),
	}

	options, err := s.listOptions(ctx, qs.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list options for question %d: %w", qs.ID, err)
	}
	qs.Options = options

	return qs, nil
}

// CreateQuestion creates a new question using a transaction.
func (s *QuizStore) CreateQuestion(ctx context.Context, qs *quiz.Question) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execCreateQuestion(ctx, q, qs)
	})
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}

	return nil
}

// DeleteQuiz deletes a quiz and all its questions and options by ID.
// Cascades to questions and options via foreign key constraints.
func (s *QuizStore) DeleteQuiz(ctx context.Context, id int64) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execDeleteQuiz(ctx, q, id)
	})
	if err != nil {
		return fmt.Errorf("failed to delete quiz: %w", err)
	}

	return nil
}

// DeleteQuestion deletes a question and all its options by ID.
// Cascades to options via foreign key constraints.
func (s *QuizStore) DeleteQuestion(ctx context.Context, id int64) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execDeleteQuestion(ctx, q, id)
	})
	if err != nil {
		return fmt.Errorf("failed to delete question: %w", err)
	}

	return nil
}

// UpdateQuestion updates a question using a transaction.
func (s *QuizStore) UpdateQuestion(ctx context.Context, qs *quiz.Question) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return s.execUpdateQuestion(ctx, q, qs)
	})
	if err != nil {
		return fmt.Errorf("failed to update question: %w", err)
	}

	return nil
}

// SetQuestionMedia patches only a question's media references (#1113), leaving
// its text, position, time limit, and options untouched. The archive importer
// uses it to wire each restored question to its newly assigned media.
func (s *QuizStore) SetQuestionMedia(
	ctx context.Context, questionID int64, imageMediaID, audioMediaID *int64, audioRepeat bool,
) error {
	res, err := s.q.SetQuestionMedia(ctx, db.SetQuestionMediaParams{
		ImageMediaID: nullableInt64(imageMediaID),
		AudioMediaID: nullableInt64(audioMediaID),
		AudioRepeat:  boolToInt64(audioRepeat),
		ID:           questionID,
	})
	if err != nil {
		return fmt.Errorf("failed to set question media: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingQuestionNoRowsAffected
	}

	return nil
}

// createQuestionAtNextPositionRetries caps the optimistic retry loop in
// [QuizStore.CreateQuestionAtNextPosition] so two genuinely concurrent
// callers eventually serialize without spinning forever. Three attempts
// covers the realistic admin-double-click case (one slot collision)
// while still giving up loudly if something stranger is going on.
const createQuestionAtNextPositionRetries = 3

// CreateQuestionAtNextPosition reads max(position)+1 and inserts the
// question with that position inside a single transaction. The UNIQUE
// INDEX on questions(quiz_id, position) (#352) catches the race when two
// transactions both pick the same slot; this method retries up to
// [createQuestionAtNextPositionRetries] times so genuinely concurrent
// admin clicks succeed without surfacing the constraint to the caller.
//
// Returns [quiz.ErrCreatingQuestion] (wrapped) on any non-conflict
// failure; surfaces the raw SQLite UNIQUE error after the retry budget
// is exhausted so callers can distinguish "system busy" from "broken
// invariant".
func (s *QuizStore) CreateQuestionAtNextPosition(ctx context.Context, qs *quiz.Question) error {
	var lastErr error
	for range createQuestionAtNextPositionRetries {
		txErr := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
			maxPos, err := q.MaxQuestionPosition(ctx, qs.QuizID)
			if err != nil {
				return fmt.Errorf("read max question position: %w", err)
			}
			qs.Position = int(maxPos) + 1

			return s.execCreateQuestion(ctx, q, qs)
		})
		if txErr == nil {
			return nil
		}
		var sqliteErr *sqlite.Error
		if errors.As(txErr, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			lastErr = txErr

			continue
		}

		return fmt.Errorf("failed to create question at next position: %w", txErr)
	}

	return fmt.Errorf(
		"failed to create question after %d position retries: %w",
		createQuestionAtNextPositionRetries,
		lastErr,
	)
}

// SwapQuestionPositions atomically swaps the question's position with
// its neighbour on the given side. The pair runs in one transaction
// so a concurrent read never sees a half-swapped state.
func (s *QuizStore) SwapQuestionPositions(
	ctx context.Context, quizID, questionID int64, direction string,
) error {
	if direction != quiz.DirectionUp && direction != quiz.DirectionDown {
		return quiz.ErrInvalidDirection
	}

	rows, err := s.q.ListQuestionsByQuizID(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list questions for swap: %w", err)
	}

	idx := -1
	for i := range rows {
		if rows[i].ID == questionID {
			idx = i

			break
		}
	}
	if idx == -1 {
		return quiz.ErrQuestionNotFound
	}

	var neighbourIdx int
	switch direction {
	case quiz.DirectionUp:
		if idx == 0 {
			return quiz.ErrQuestionAtTop
		}
		neighbourIdx = idx - 1
	case quiz.DirectionDown:
		if idx == len(rows)-1 {
			return quiz.ErrQuestionAtBottom
		}
		neighbourIdx = idx + 1
	default:
		// Guarded above by the early return for invalid directions,
		// so this branch is unreachable. The explicit default keeps
		// revive's enforce-switch-style happy.
		return quiz.ErrInvalidDirection
	}

	current, neighbour := rows[idx], rows[neighbourIdx]

	err = database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return execSwapQuestionPositions(ctx, q, current.ID, current.Position, neighbour.ID, neighbour.Position)
	})
	if err != nil {
		return fmt.Errorf("failed to swap question positions: %w", err)
	}

	return nil
}

// execSwapQuestionPositions is the three-step swap body of
// [QuizStore.SwapQuestionPositions]; pulled out so the public method
// stays under revive's function-length limit. SQLite checks
// UNIQUE(quiz_id, position) per statement (no deferred uniqueness),
// so a naive two-step swap trips the constraint on the first UPDATE.
// Park the current row at -current.ID - guaranteed unique because IDs
// are positive - move the neighbour into the current row's slot,
// then settle the current row into the neighbour's old slot.
func execSwapQuestionPositions(
	ctx context.Context, q *db.Queries, currentID, currentPos, neighbourID, neighbourPos int64,
) error {
	if _, err := q.UpdateQuestionPosition(ctx, db.UpdateQuestionPositionParams{
		Position: -currentID,
		ID:       currentID,
	}); err != nil {
		return fmt.Errorf("park current question position: %w", err)
	}
	if _, err := q.UpdateQuestionPosition(ctx, db.UpdateQuestionPositionParams{
		Position: currentPos,
		ID:       neighbourID,
	}); err != nil {
		return fmt.Errorf("update neighbour question position: %w", err)
	}
	if _, err := q.UpdateQuestionPosition(ctx, db.UpdateQuestionPositionParams{
		Position: neighbourPos,
		ID:       currentID,
	}); err != nil {
		return fmt.Errorf("update current question position: %w", err)
	}

	return nil
}

// MoveQuestionToPosition moves a question to a 1-based slot within a
// target round (which may differ from its current round), then recomputes
// every question's quiz-wide position so the questions of each round stay
// contiguous and in round-position order, dense 1..N. The validation,
// reassignment, and renumber share a transaction so a concurrent edit
// cannot leave a half-moved state (#199).
func (s *QuizStore) MoveQuestionToPosition(
	ctx context.Context, quizID, questionID, targetRoundID int64, newPosition int,
) error {
	if err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return moveQuestionToPositionTx(ctx, q, quizID, questionID, targetRoundID, newPosition)
	}); err != nil {
		return fmt.Errorf("failed to move question to position: %w", err)
	}

	return nil
}

// moveQuestionToPositionTx is the transactional body of
// MoveQuestionToPosition. Pulled out so the public method stays under
// revive's cognitive-complexity budget and the sentinel errors are not
// wrapped on the way out.
func moveQuestionToPositionTx(
	ctx context.Context, q *db.Queries, quizID, questionID, targetRoundID int64, newPosition int,
) error {
	if err := validateQuestionMove(ctx, q, quizID, questionID, targetRoundID); err != nil {
		return err
	}

	rounds, err := q.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list rounds for question move: %w", err)
	}
	questions, err := q.ListQuestionsByQuizID(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to list questions for question move: %w", err)
	}

	ordered := reorderQuestionsForMove(rounds, questions, questionID, targetRoundID, newPosition)

	if _, err = q.MoveQuestionToRound(ctx, db.MoveQuestionToRoundParams{
		RoundID: targetRoundID,
		ID:      questionID,
	}); err != nil {
		return fmt.Errorf("update question round: %w", err)
	}

	ids := make([]int64, len(ordered))
	current := make([]int64, len(ordered))
	for i, qs := range ordered {
		ids[i], current[i] = qs.ID, qs.Position
	}

	return renumberDensePositions(ctx, ids, current, func(ctx context.Context, id, pos int64) error {
		if _, err := q.UpdateQuestionPosition(ctx, db.UpdateQuestionPositionParams{Position: pos, ID: id}); err != nil {
			return fmt.Errorf("update question position: %w", err)
		}

		return nil
	})
}

// validateQuestionMove confirms the question and target round both exist
// and belong to quizID, mirroring moveQuestionToRoundTx's cross-quiz IDOR
// gate.
func validateQuestionMove(ctx context.Context, q *db.Queries, quizID, questionID, targetRoundID int64) error {
	question, err := q.GetQuestion(ctx, questionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quiz.ErrQuestionNotFound
		}

		return fmt.Errorf("load question for position move: %w", err)
	}
	if question.QuizID != quizID {
		return quiz.ErrQuestionNotFound
	}

	round, err := q.GetRound(ctx, targetRoundID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quiz.ErrRoundNotFound
		}

		return fmt.Errorf("load round for position move: %w", err)
	}
	if round.QuizID != quizID {
		return quiz.ErrRoundNotFound
	}

	return nil
}

// reorderQuestionsForMove builds the final quiz-wide question order after
// moving questionID into targetRoundID at the 1-based newPosition. It
// groups questions by round (preserving the position order they arrive
// in), removes the moved question from its current round's list, inserts
// it into the target round's list at the clamped index, then flattens the
// rounds in position order. The returned slice is the desired order;
// the caller assigns the dense 1..N positions via renumberDensePositions.
func reorderQuestionsForMove(
	rounds []db.Round, questions []db.Question, questionID, targetRoundID int64, newPosition int,
) []db.Question {
	byRound := make(map[int64][]db.Question, len(rounds))
	var moved db.Question
	for _, qs := range questions {
		if qs.ID == questionID {
			moved = qs

			continue
		}
		byRound[qs.RoundID] = append(byRound[qs.RoundID], qs)
	}

	targetList := byRound[targetRoundID]
	target := clampIndex(newPosition, len(targetList))
	byRound[targetRoundID] = slices.Insert(targetList, target, moved)

	ordered := make([]db.Question, 0, len(questions))
	for _, rnd := range rounds {
		ordered = append(ordered, byRound[rnd.ID]...)
	}

	return ordered
}

// GetOption retrieves an option by its ID from the data store and returns it. Returns ErrOptionNotFound if no option is found.
func (s *QuizStore) GetOption(ctx context.Context, optionID int64) (*quiz.Option, error) {
	row, err := s.q.GetOption(ctx, optionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, quiz.ErrOptionNotFound
		}

		return nil, fmt.Errorf("failed to get option: %w", err)
	}

	option := &quiz.Option{
		ID:         row.ID,
		QuestionID: row.QuestionID,
		Text:       row.Text,
		Correct:    row.IsCorrect,
	}

	return option, nil
}

// GetOptionsByIDs retrieves options for the given IDs from the data store.
func (s *QuizStore) GetOptionsByIDs(ctx context.Context, ids []int64) ([]*quiz.Option, error) {
	rows, err := s.q.GetOptionsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to get options by IDs: %w", err)
	}

	options := make([]*quiz.Option, 0, len(rows))
	for _, row := range rows {
		options = append(options, &quiz.Option{
			ID:         row.ID,
			QuestionID: row.QuestionID,
			Text:       row.Text,
			Correct:    row.IsCorrect,
		})
	}

	return options, nil
}

// classifySlugConflictErr maps a CreateQuiz / UpdateQuiz storage error
// onto [quiz.ErrSlugTaken] when the underlying SQLite failure is a
// UNIQUE-constraint violation. `slug` is the only UNIQUE column on the
// quizzes table (see migration 20251201084529 / 20260520200000), so a
// SQLITE_CONSTRAINT_UNIQUE on this path can only mean a slug collision -
// the classifier doesn't need to inspect the error message to
// disambiguate. The wrapped err is still returned via %w so callers
// using [errors.Is] can still recover the original sqlite.Error if
// they need details for logging (#293).
func classifySlugConflictErr(err error, op string) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return quiz.ErrSlugTaken
	}

	return fmt.Errorf("%s: %w", op, err)
}

func (s *QuizStore) execCreateQuiz(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	if qz.CreatedByPlayerID == 0 {
		return quiz.ErrCreatorRequired
	}
	// The migration's DB DEFAULT only fires for INSERTs that omit the
	// column; we always supply both. Backfill the project-wide values
	// here so test fixtures and the JSON-import path don't have to
	// repeat them (#99 / #103).
	timeLimit := qz.TimeLimitSeconds
	if timeLimit == 0 {
		timeLimit = quiz.DefaultTimeLimitSeconds
	}
	visibility, mode, language := quiz.NormalizedFields(qz)
	row, err := q.CreateQuiz(ctx, db.CreateQuizParams{
		Title:             qz.Title,
		Slug:              qz.Slug,
		Description:       qz.Description,
		CreatedByPlayerID: qz.CreatedByPlayerID,
		TimeLimitSeconds:  int64(timeLimit),
		Visibility:        visibility,
		Mode:              mode,
		Language:          language,
		// New quizzes default to draft; seed callers (fixtures, importers) set Published explicitly (#1192).
		Published: boolToInt64(qz.Published),
	})
	if err != nil {
		return classifySlugConflictErr(err, "failed to create quiz")
	}

	qz.ID = row.ID
	qz.CreatedAt = row.CreatedAt
	qz.UpdatedAt = row.UpdatedAt
	qz.TimeLimitSeconds = int(row.TimeLimitSeconds)
	qz.Visibility = row.Visibility
	qz.Mode = row.Mode
	qz.Language = row.Language
	qz.PlayCount = row.PlayCount
	qz.Published = row.Published != 0

	// Every quiz needs a default round (#444): questions.round_id is NOT
	// NULL and execCreateQuestion resolves it via GetDefaultRound.
	// The migration only backfills rounds for quizzes that existed at
	// migrate time, so newly created quizzes must seed their own here.
	if _, err = q.CreateRound(ctx, db.CreateRoundParams{
		QuizID:   qz.ID,
		Position: 0,
		Title:    defaultRoundTitle,
	}); err != nil {
		return fmt.Errorf("failed to create default question round: %w", err)
	}

	if len(qz.Rounds) > 0 {
		if err = s.createAuthoredRounds(ctx, q, qz); err != nil {
			return fmt.Errorf("failed to create authored rounds: %w", err)
		}

		return nil
	}

	for _, qs := range qz.Questions {
		qs.ID = 0
		qs.QuizID = qz.ID
	}

	if err = s.handleQuestions(ctx, q, qz); err != nil {
		return fmt.Errorf("failed to handle questions: %w", err)
	}

	return nil
}

// createAuthoredRounds persists a quiz whose rounds were authored
// explicitly (the JSON-import rounds[] path, #546). The first authored
// round reuses the default round seeded just above - renamed in place to
// the authored title and summary - so the quiz never ends up with a
// stray empty "Round 1" alongside the authored ones. The remaining
// rounds are created at positions 1..N. Each round's questions are
// inserted with that round's id, preserving their quiz-wide Position.
func (s *QuizStore) createAuthoredRounds(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	defaultRound, err := q.GetDefaultRound(ctx, qz.ID)
	if err != nil {
		return fmt.Errorf("failed to resolve default round for quiz %d: %w", qz.ID, err)
	}

	for i, round := range qz.Rounds {
		roundID := defaultRound.ID
		if i == 0 {
			if _, err = q.UpdateRound(ctx, db.UpdateRoundParams{
				Title:                   round.Title,
				Summary:                 round.Summary,
				Position:                defaultRound.Position,
				BoundaryDurationSeconds: nullableInt(round.BoundaryDurationSeconds),
				ID:                      defaultRound.ID,
			}); err != nil {
				return fmt.Errorf("failed to rename default round: %w", err)
			}
		} else {
			row, createErr := q.CreateRound(ctx, db.CreateRoundParams{
				QuizID:                  qz.ID,
				Position:                int64(i),
				Title:                   round.Title,
				Summary:                 round.Summary,
				BoundaryDurationSeconds: nullableInt(round.BoundaryDurationSeconds),
			})
			if createErr != nil {
				return fmt.Errorf("failed to create round %q: %w", round.Title, createErr)
			}
			roundID = row.ID
		}

		for _, qs := range round.Questions {
			qs.ID = 0
			qs.QuizID = qz.ID
			qs.RoundID = roundID
			if err = s.execCreateQuestion(ctx, q, qs); err != nil {
				return fmt.Errorf("failed to create question in round %q: %w", round.Title, err)
			}
		}
	}

	return nil
}

func (s *QuizStore) execUpdateQuiz(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	if qz.ID == 0 {
		return quiz.ErrCannotUpdateQuizWithIDZero
	}

	visibility, mode, language := quiz.NormalizedFields(qz)
	var err error
	timeLimit := qz.TimeLimitSeconds
	if timeLimit == 0 {
		timeLimit = quiz.DefaultTimeLimitSeconds
	}
	res, err := q.UpdateQuiz(ctx, db.UpdateQuizParams{
		Title:            qz.Title,
		Slug:             qz.Slug,
		Description:      qz.Description,
		TimeLimitSeconds: int64(timeLimit),
		Visibility:       visibility,
		Mode:             mode,
		Language:         language,
		ID:               qz.ID,
	})
	if err != nil {
		return classifySlugConflictErr(err, "failed to update quiz")
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingQuizNoRowsAffected
	}

	for _, qs := range qz.Questions {
		qs.QuizID = qz.ID
	}

	if err = s.handleQuestions(ctx, q, qz); err != nil {
		return fmt.Errorf("failed to handle questions: %w", err)
	}

	return nil
}

func (s *QuizStore) handleQuestions(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	var err error
	existingIDs, err := q.ListQuestionIDsByQuizID(ctx, qz.ID)
	if err != nil {
		return fmt.Errorf("failed to list existing question IDs for quiz %d: %w", qz.ID, err)
	}

	incomingIDs := make(map[int64]bool)
	for _, qs := range qz.Questions {
		if qs.ID == 0 {
			// CREATE
			if createErr := s.execCreateQuestion(ctx, q, qs); createErr != nil {
				return fmt.Errorf("failed to create question: %w", createErr)
			}
		} else {
			// UPDATE
			incomingIDs[qs.ID] = true

			if updateErr := s.execUpdateQuestion(ctx, q, qs); updateErr != nil {
				return fmt.Errorf("failed to update question: %w", updateErr)
			}
		}
	}

	// DELETE
	deleteIDs := make([]int64, 0, len(existingIDs))
	for _, id := range existingIDs {
		if !incomingIDs[id] {
			deleteIDs = append(deleteIDs, id)
		}
	}

	if err = s.execDeleteQuestions(ctx, q, deleteIDs); err != nil {
		return fmt.Errorf("failed to delete questions: %w", err)
	}

	return nil
}

// execCreateQuestion creates a new question. questions.round_id is NOT
// NULL (#444); when the caller leaves RoundID zero, resolve it to the
// quiz's default (lowest-position) round so slice-1 callers don't have
// to pick a round. Slice 3 adds real round selection.
func (s *QuizStore) execCreateQuestion(ctx context.Context, q *db.Queries, qs *quiz.Question) error {
	if qs.RoundID == 0 {
		round, err := q.GetDefaultRound(ctx, qs.QuizID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// No default round means the quiz does not exist (or has
				// no rounds); map it to the sentinel so callers can tell
				// it apart from an infra failure.
				return quiz.ErrRoundNotFound
			}

			return fmt.Errorf("failed to resolve default question round for quiz %d: %w", qs.QuizID, err)
		}
		qs.RoundID = round.ID
	}

	row, err := q.CreateQuestion(ctx, db.CreateQuestionParams{
		QuizID:           qs.QuizID,
		RoundID:          qs.RoundID,
		Text:             qs.Text,
		Position:         int64(qs.Position),
		ImageMediaID:     nullableInt64(qs.ImageMediaID),
		AudioMediaID:     nullableInt64(qs.AudioMediaID),
		AudioRepeat:      boolToInt64(qs.AudioRepeat),
		TimeLimitSeconds: nullableInt(qs.TimeLimitSeconds),
	})
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}

	qs.ID = row.ID
	qs.RoundID = row.RoundID
	qs.AudioRepeat = row.AudioRepeat != 0
	qs.TimeLimitSeconds = nullableIntToPtr(row.TimeLimitSeconds)
	for _, o := range qs.Options {
		o.ID = 0
		o.QuestionID = qs.ID
	}

	if err = s.handleOptions(ctx, q, qs); err != nil {
		return fmt.Errorf("failed to handle options: %w", err)
	}

	return nil
}

func (s *QuizStore) execUpdateQuestion(ctx context.Context, q *db.Queries, qs *quiz.Question) error {
	if qs.ID == 0 {
		return quiz.ErrCannotUpdateQuestionWithIDZero
	}

	var err error
	res, err := q.UpdateQuestion(ctx, db.UpdateQuestionParams{
		Text:             qs.Text,
		Position:         int64(qs.Position),
		ImageMediaID:     nullableInt64(qs.ImageMediaID),
		AudioMediaID:     nullableInt64(qs.AudioMediaID),
		AudioRepeat:      boolToInt64(qs.AudioRepeat),
		TimeLimitSeconds: nullableInt(qs.TimeLimitSeconds),
		ID:               qs.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update question: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingQuestionNoRowsAffected
	}

	for _, o := range qs.Options {
		o.QuestionID = qs.ID
	}

	if err = s.handleOptions(ctx, q, qs); err != nil {
		return fmt.Errorf("failed to handle options: %w", err)
	}

	return nil
}

func (*QuizStore) execDeleteQuiz(ctx context.Context, q *db.Queries, id int64) error {
	// The cascade only covers questions -> options. The game_* tables
	// reference quizzes (via games.quiz_id), questions (via
	// game_questions.question_id), and options (via game_answers.option_id)
	// without ON DELETE CASCADE, so they would block the quiz row delete
	// once the quiz has been played. Snapshot the affected game IDs and
	// drop the dependent rows explicitly, in the same order the
	// player-on-quiz reset uses, before deleting the quiz itself.
	gameIDs, err := q.ListGameIDsForQuiz(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to list game IDs for quiz %d: %w", id, err)
	}
	if len(gameIDs) > 0 {
		if err = q.DeleteGameAnswersByGameIDs(ctx, gameIDs); err != nil {
			return fmt.Errorf("failed to delete game answers: %w", err)
		}
		if err = q.DeleteGameQuestionsByGameIDs(ctx, gameIDs); err != nil {
			return fmt.Errorf("failed to delete game questions: %w", err)
		}
		if err = q.DeleteGameParticipantsByGameIDs(ctx, gameIDs); err != nil {
			return fmt.Errorf("failed to delete game participants: %w", err)
		}
		if err = q.DeleteGamesByIDs(ctx, gameIDs); err != nil {
			return fmt.Errorf("failed to delete games: %w", err)
		}
	}

	res, err := q.DeleteQuiz(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete quiz: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingQuizNoRowsAffected
	}

	return nil
}

func (*QuizStore) execDeleteQuestion(ctx context.Context, q *db.Queries, id int64) error {
	// game_questions.question_id and game_answers.option_id /
	// game_answers.game_question_id reference this question (and its
	// options) without ON DELETE CASCADE, so once the question has been
	// played SQLite raises FOREIGN KEY constraint failed (787) on the
	// raw question delete. Snapshot the affected game_question IDs and
	// drop the dependent game_answers / game_questions rows first.
	// Filtering the answer delete by game_question_id (not game_id) is
	// deliberate: a single game can hold answers for many questions, and
	// dropping by game_id would over-delete answers for OTHER questions
	// in the same game. Options cascade from the question itself.
	gqIDs, err := q.ListGameQuestionIDsForQuestion(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to list game question IDs for question %d: %w", id, err)
	}
	if len(gqIDs) > 0 {
		if err = q.DeleteGameAnswersByGameQuestionIDs(ctx, gqIDs); err != nil {
			return fmt.Errorf("failed to delete game answers: %w", err)
		}
		if err = q.DeleteGameQuestionsByQuestionID(ctx, id); err != nil {
			return fmt.Errorf("failed to delete game questions: %w", err)
		}
	}

	res, err := q.DeleteQuestion(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete question: %w", err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingQuestionNoRowsAffected
	}

	return nil
}

func (s *QuizStore) execDeleteQuestions(ctx context.Context, q *db.Queries, ids []int64) error {
	for _, id := range ids {
		if err := s.execDeleteQuestion(ctx, q, id); err != nil {
			return fmt.Errorf("failed to delete question %d: %w", id, err)
		}
	}

	return nil
}

func (s *QuizStore) listOptions(ctx context.Context, questionID int64) ([]*quiz.Option, error) {
	rows, err := s.q.ListOptionsByQuestionID(ctx, questionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list options for question %d: %w", questionID, err)
	}

	options := make([]*quiz.Option, 0, len(rows))
	for _, r := range rows {
		options = append(options, &quiz.Option{
			ID:         r.ID,
			QuestionID: r.QuestionID,
			Text:       r.Text,
			Correct:    r.IsCorrect,
		})
	}

	return options, nil
}

// listOptionsByQuiz fetches every option for a quiz in one query and
// groups them by question ID, so ListQuestions avoids one option query
// per question on the hot read path. Per-question order matches
// listOptions (ascending option ID).
func (s *QuizStore) listOptionsByQuiz(ctx context.Context, quizID int64) (map[int64][]*quiz.Option, error) {
	rows, err := s.q.ListOptionsByQuizID(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list options for quiz %d: %w", quizID, err)
	}

	optionsByQuestion := make(map[int64][]*quiz.Option)
	for _, r := range rows {
		optionsByQuestion[r.QuestionID] = append(optionsByQuestion[r.QuestionID], &quiz.Option{
			ID:         r.ID,
			QuestionID: r.QuestionID,
			Text:       r.Text,
			Correct:    r.IsCorrect,
		})
	}

	return optionsByQuestion, nil
}

func (s *QuizStore) handleOptions(ctx context.Context, q *db.Queries, qs *quiz.Question) error {
	existingIDs, err := q.ListOptionIDsByQuestionID(ctx, qs.ID)
	if err != nil {
		return fmt.Errorf("failed to list existing option IDs for question %d: %w", qs.ID, err)
	}

	incomingIDs := make(map[int64]bool)
	for _, o := range qs.Options {
		if o.ID == 0 {
			// CREATE
			if createErr := s.createOption(ctx, q, o); createErr != nil {
				return fmt.Errorf("failed to create option: %w", createErr)
			}
		} else {
			// UPDATE
			incomingIDs[o.ID] = true

			if updateErr := s.updateOption(ctx, q, qs.ID, o); updateErr != nil {
				return fmt.Errorf("failed to update option: %w", updateErr)
			}
		}
	}

	deleteIDs := make([]int64, 0, len(existingIDs))
	for _, id := range existingIDs {
		if !incomingIDs[id] {
			deleteIDs = append(deleteIDs, id)
		}
	}

	if err = s.deleteOptions(ctx, q, qs.ID, deleteIDs); err != nil {
		return fmt.Errorf("failed to delete options: %w", err)
	}

	return nil
}

func (*QuizStore) createOption(ctx context.Context, q *db.Queries, o *quiz.Option) error {
	row, err := q.CreateOption(ctx, db.CreateOptionParams{
		QuestionID: o.QuestionID,
		Text:       o.Text,
		IsCorrect:  o.Correct,
	})
	if err != nil {
		return fmt.Errorf("failed to create option: %w", err)
	}

	o.ID = row.ID

	return nil
}

func (*QuizStore) updateOption(ctx context.Context, q *db.Queries, questionID int64, o *quiz.Option) error {
	res, err := q.UpdateOption(ctx, db.UpdateOptionParams{
		Text:       o.Text,
		IsCorrect:  o.Correct,
		ID:         o.ID,
		QuestionID: questionID,
	})
	if err != nil {
		return fmt.Errorf("failed to update option: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingOptionNoRowsAffected
	}

	return nil
}

func (s *QuizStore) deleteOptions(ctx context.Context, q *db.Queries, questionID int64, ids []int64) error {
	for _, id := range ids {
		if err := s.deleteOption(ctx, q, questionID, id); err != nil {
			return fmt.Errorf("failed to delete option %d: %w", id, err)
		}
	}

	return nil
}

func (*QuizStore) deleteOption(ctx context.Context, q *db.Queries, questionID, id int64) error {
	res, err := q.DeleteOption(ctx, db.DeleteOptionParams{
		ID:         id,
		QuestionID: questionID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete option %d: %w", id, err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingOptionNoRowsAffected
	}

	return nil
}

// nullableInt packs a *int into the [sql.NullInt64] the sqlc-generated params
// expect for nullable integer columns such as questions.time_limit_seconds
// (nil -> "inherit the quiz default", #99) and media.duration_ms (nil ->
// "unknown length"). nil maps to NULL.
func nullableInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

// nullableIntToPtr is the inverse of nullableInt, used when hydrating domain
// values from sqlc RETURNING / SELECT rows; NULL maps back to nil so "unset"
// stays distinct from a real zero.
func nullableIntToPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	out := int(v.Int64)

	return &out
}

// nullableInt64 packs a *int64 into the [sql.NullInt64] the sqlc-generated
// params expect for questions.image_media_id. nil -> NULL, which means "no image
// attached" (#937).
func nullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: *v, Valid: true}
}

// nullableInt64ToPtr is the inverse of nullableInt64, used when hydrating
// Question.ImageMediaID from sqlc RETURNING / SELECT rows.
func nullableInt64ToPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64

	return &out
}

// boolToInt64 maps a Go bool onto the 0/1 INTEGER column sqlc generates as
// int64 (e.g. questions.audio_repeat).
//
//nolint:revive // v is the value being converted, not a behavioural mode switch.
func boolToInt64(v bool) int64 {
	if v {
		return 1
	}

	return 0
}
