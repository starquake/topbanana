package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

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

// ListQuizzes returns a list of quizzes.
func (s *QuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	var err error
	rows, err := s.q.ListQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list quizzes: %w", err)
	}

	quizzes := make([]*quiz.Quiz, 0, len(rows))
	for _, r := range rows {
		qz := &quiz.Quiz{
			ID:          r.ID,
			Title:       r.Title,
			Slug:        r.Slug,
			Description: r.Description,
			CreatedAt:   r.CreatedAt,
		}

		qz.Questions, err = s.ListQuestions(ctx, qz.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to list questions for quiz %d: %w", qz.ID, err)
		}

		quizzes = append(quizzes, qz)
	}

	return quizzes, nil
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
		ID:          row.ID,
		Title:       row.Title,
		Slug:        row.Slug,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
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
	err := database.ExecTx(s.db, ctx, func(q *db.Queries) error {
		return s.execCreateQuiz(ctx, q, qz)
	})
	if err != nil {
		return fmt.Errorf("failed to create quiz: %w", err)
	}

	return nil
}

// UpdateQuiz updates a quiz using a transaction.
func (s *QuizStore) UpdateQuiz(ctx context.Context, qz *quiz.Quiz) error {
	err := database.ExecTx(s.db, ctx, func(q *db.Queries) error {
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
			ID:       r.ID,
			QuizID:   r.QuizID,
			Text:     r.Text,
			Position: int(r.Position),
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
		ID:       row.ID,
		QuizID:   row.QuizID,
		Text:     row.Text,
		Position: int(row.Position),
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
	err := database.ExecTx(s.db, ctx, func(q *db.Queries) error {
		return s.execCreateQuestion(ctx, q, qs)
	})
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}

	return nil
}

// UpdateQuestion updates a question using a transaction.
func (s *QuizStore) UpdateQuestion(ctx context.Context, qs *quiz.Question) error {
	err := database.ExecTx(s.db, ctx, func(q *db.Queries) error {
		return s.execUpdateQuestion(ctx, q, qs)
	})
	if err != nil {
		return fmt.Errorf("failed to update question: %w", err)
	}

	return nil
}

// mustRowsAffected is a helper to panic if the result of a query has no rows affected.
// TODO: Move to database package.
func mustRowsAffected(res sql.Result) int64 {
	rows, err := res.RowsAffected()
	if err != nil {
		panic(err)
	}

	return rows
}

func (s *QuizStore) execCreateQuiz(ctx context.Context, q *db.Queries, qz *quiz.Quiz) error {
	var err error
	row, err := q.CreateQuiz(ctx, db.CreateQuizParams{
		Title:       qz.Title,
		Slug:        qz.Slug,
		Description: qz.Description,
	})
	if err != nil {
		return fmt.Errorf("failed to create quiz: %w", err)
	}

	qz.ID = row.ID
	qz.CreatedAt = row.CreatedAt

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

	var err error
	res, err := q.UpdateQuiz(ctx, db.UpdateQuizParams{
		Title:       qz.Title,
		Slug:        qz.Slug,
		Description: qz.Description,
		ID:          qz.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update quiz: %w", err)
	}

	if mustRowsAffected(res) == 0 {
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

			// Create a map of incoming question IDs and track which IDs should remain to exist
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

	if err = s.deleteQuestions(ctx, q, deleteIDs); err != nil {
		return fmt.Errorf("failed to delete questions: %w", err)
	}

	return err
}

// execCreateQuestion creates a new question.
func (s *QuizStore) execCreateQuestion(ctx context.Context, q *db.Queries, qs *quiz.Question) error {
	row, err := q.CreateQuestion(ctx, db.CreateQuestionParams{
		QuizID:   qs.QuizID,
		Text:     qs.Text,
		Position: int64(qs.Position),
	})
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}

	qs.ID = row.ID
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
		Text:     qs.Text,
		Position: int64(qs.Position),
		ID:       qs.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update question: %w", err)
	}

	if mustRowsAffected(res) == 0 {
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

func (*QuizStore) deleteQuestion(ctx context.Context, q *db.Queries, id int64) error {
	res, err := q.DeleteQuestion(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete question: %w", err)
	}

	if mustRowsAffected(res) == 0 {
		return quiz.ErrDeletingQuestionNoRowsAffected
	}

	return nil
}

func (s *QuizStore) deleteQuestions(ctx context.Context, q *db.Queries, ids []int64) error {
	for _, id := range ids {
		if err := s.deleteQuestion(ctx, q, id); err != nil {
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

			// Create a map of incoming question IDs and track which IDs should remain to exist
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
	if mustRowsAffected(res) == 0 {
		return quiz.ErrUpdatingOptionNoRowsAffected
	}
	if err != nil {
		return fmt.Errorf("failed to update option: %w", err)
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

	if mustRowsAffected(res) == 0 {
		return quiz.ErrDeletingOptionNoRowsAffected
	}

	return nil
}
