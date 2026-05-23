package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

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
// Includes rows of every visibility — use [QuizStore.ListPublicQuizzes] from
// public-facing handlers.
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
			// INNER JOIN on players makes this a plain string (#359);
			// the FK guarantees a creator row exists.
			CreatedByUsername: r.CreatedByUsername,
		}
		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
}

// ListPublicQuizzes returns the visibility=public subset of
// [QuizStore.ListQuizzes] (#103). Same shape, same ordering — just the
// rows safe to surface to anonymous traffic.
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
			// INNER JOIN, see ListQuizzes (#359).
			CreatedByUsername: r.CreatedByUsername,
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
	var err error
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
		// INNER JOIN, see ListQuizzes (#359).
		CreatedByUsername: row.CreatedByUsername,
	}

	questions, err := s.ListQuestions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list questions for quiz %d: %w", id, err)
	}
	qz.Questions = questions

	return qz, nil
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

// ListQuestions retrieves a list of questions for the specified quiz ID, including their options, from the data store.
func (s *QuizStore) ListQuestions(ctx context.Context, quizID int64) ([]*quiz.Question, error) {
	var err error
	rows, err := s.q.ListQuestionsByQuizID(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list questions for quiz %d: %w", quizID, err)
	}

	questions := make([]*quiz.Question, 0, len(rows))
	for _, r := range rows {
		qs := &quiz.Question{
			ID:               r.ID,
			QuizID:           r.QuizID,
			Text:             r.Text,
			Position:         int(r.Position),
			ImageURL:         r.ImageUrl,
			TimeLimitSeconds: nullableTimeLimitToPtr(r.TimeLimitSeconds),
		}

		options, listErr := s.listOptions(ctx, qs.ID)
		if listErr != nil {
			return nil, fmt.Errorf("failed to list options for question %d: %w", qs.ID, listErr)
		}
		qs.Options = options

		questions = append(questions, qs)
	}

	return questions, nil
}

// GetQuestion retrieves a question by its ID, including its options, from the data store or returns an appropriate error.
func (s *QuizStore) GetQuestion(ctx context.Context, id int64) (*quiz.Question, error) {
	var err error
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
		Text:             row.Text,
		Position:         int(row.Position),
		ImageURL:         row.ImageUrl,
		TimeLimitSeconds: nullableTimeLimitToPtr(row.TimeLimitSeconds),
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

// SwapQuestionPositions swaps the questionID's position with its
// neighbour on the given side within the same quiz, atomically. The
// implementation reads the full ordered list once and finds the
// adjacent row in Go rather than via SQL — quizzes hold a handful of
// questions in practice, so the simpler code beats a second LIMIT/
// ORDER query. Both position updates run inside a single transaction
// so a concurrent read never observes a half-swapped state.
//
// Returns [quiz.ErrInvalidDirection] when direction is neither "up"
// nor "down", [quiz.ErrQuestionNotFound] when the question does not
// belong to the quiz, and [quiz.ErrQuestionAtTop] /
// [quiz.ErrQuestionAtBottom] when the question is already at the
// requested boundary.
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
// Park the current row at -current.ID — guaranteed unique because IDs
// are positive — move the neighbour into the current row's slot,
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

// GetOption retrieves an option by its ID from the data store and returns it. Returns ErrOptionNotFound if no option is found.
func (s *QuizStore) GetOption(ctx context.Context, optionID int64) (*quiz.Option, error) {
	var err error
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
// SQLITE_CONSTRAINT_UNIQUE on this path can only mean a slug collision —
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
	visibility := qz.Visibility
	if visibility == "" {
		visibility = quiz.VisibilityPublic
	}
	row, err := q.CreateQuiz(ctx, db.CreateQuizParams{
		Title:             qz.Title,
		Slug:              qz.Slug,
		Description:       qz.Description,
		CreatedByPlayerID: qz.CreatedByPlayerID,
		TimeLimitSeconds:  int64(timeLimit),
		Visibility:        visibility,
	})
	if err != nil {
		return classifySlugConflictErr(err, "failed to create quiz")
	}

	qz.ID = row.ID
	qz.CreatedAt = row.CreatedAt
	qz.UpdatedAt = row.UpdatedAt
	qz.TimeLimitSeconds = int(row.TimeLimitSeconds)
	qz.Visibility = row.Visibility

	for _, qs := range qz.Questions {
		qs.ID = 0
		qs.QuizID = qz.ID
	}

	if err = s.handleQuestions(ctx, q, qz); err != nil {
		return fmt.Errorf("failed to handle questions: %w", err)
	}

	return nil
}

func (s *QuizStore) execUpdateQuiz(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	if qz.ID == 0 {
		return quiz.ErrCannotUpdateQuizWithIDZero
	}

	visibility := qz.Visibility
	if visibility == "" {
		visibility = quiz.VisibilityPublic
	}
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

// execCreateQuestion creates a new question.
func (s *QuizStore) execCreateQuestion(ctx context.Context, q *db.Queries, qs *quiz.Question) error {
	row, err := q.CreateQuestion(ctx, db.CreateQuestionParams{
		QuizID:           qs.QuizID,
		Text:             qs.Text,
		Position:         int64(qs.Position),
		ImageUrl:         qs.ImageURL,
		TimeLimitSeconds: nullableTimeLimit(qs.TimeLimitSeconds),
	})
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}

	qs.ID = row.ID
	qs.TimeLimitSeconds = nullableTimeLimitToPtr(row.TimeLimitSeconds)
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
		ImageUrl:         qs.ImageURL,
		TimeLimitSeconds: nullableTimeLimit(qs.TimeLimitSeconds),
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

			if updateErr := s.updateOption(ctx, q, o); updateErr != nil {
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

	if err = s.deleteOptions(ctx, q, deleteIDs); err != nil {
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

func (*QuizStore) updateOption(ctx context.Context, q *db.Queries, o *quiz.Option) error {
	res, err := q.UpdateOption(ctx, db.UpdateOptionParams{
		Text:      o.Text,
		IsCorrect: o.Correct,
		ID:        o.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update option: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingOptionNoRowsAffected
	}

	return nil
}

func (s *QuizStore) deleteOptions(ctx context.Context, q *db.Queries, ids []int64) error {
	for _, id := range ids {
		if err := s.deleteOption(ctx, q, id); err != nil {
			return fmt.Errorf("failed to delete option %d: %w", id, err)
		}
	}

	return nil
}

func (*QuizStore) deleteOption(ctx context.Context, q *db.Queries, id int64) error {
	var res sql.Result
	var err error
	res, err = q.DeleteOption(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete option %d: %w", id, err)
	}

	if database.MustRowsAffected(res) == 0 {
		return quiz.ErrDeletingOptionNoRowsAffected
	}

	return nil
}

// nullableTimeLimit packs a *int into the [sql.NullInt64] the sqlc-generated
// params expect for questions.time_limit_seconds. nil → NULL, which the
// game service treats as "inherit the quiz default" (#99).
func nullableTimeLimit(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

// nullableTimeLimitToPtr is the inverse of nullableTimeLimit, used when
// hydrating Question domain values from sqlc RETURNING / SELECT rows.
func nullableTimeLimitToPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	out := int(v.Int64)

	return &out
}
