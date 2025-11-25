// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/starquake/topbanana/internal/logging"
)

// Quiz represents a quiz.
type Quiz struct {
	ID          int64
	Title       string
	Slug        string
	Description string
	CreatedAt   time.Time
	Questions   []*Question
}

// Question represents a question in a quiz.
type Question struct {
	ID       int64
	QuizID   int64
	Text     string
	ImageURL string
	Position int
	Options  []*Option
}

// Option represents an option for a question.
type Option struct {
	ID         int64
	QuestionID int64
	Text       string
	Correct    bool
}

// Store represents a store for quizzes.
// This can be implemented for different databases.
type Store interface {
	// GetQuizByID returns a quiz with questions and options, by its question ID.
	GetQuizByID(ctx context.Context, id int64) (*Quiz, error)
	// GetQuestionByID returns a question with options, by its question ID.
	GetQuestionByID(ctx context.Context, id int64) (*Question, error)
	// ListQuizzes returns all quizzes.
	ListQuizzes(ctx context.Context) ([]*Quiz, error)
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, quiz *Quiz) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, question *Question) error
}

// SQLiteStore is a store for quizzes in SQLite.
type SQLiteStore struct {
	db     *sql.DB
	logger *logging.Logger
}

var (
	// ErrQuizNotFound is returned when a quiz is not found.
	ErrQuizNotFound = errors.New("quiz not found")
	// ErrQuestionNotFound is returned when a question is not found.
	ErrQuestionNotFound = errors.New("quiz not found")
)

// NewSQLiteStore creates a new SQLiteStore.
func NewSQLiteStore(db *sql.DB, logger *logging.Logger) *SQLiteStore {
	return &SQLiteStore{db, logger}
}

// GetQuizByID returns a quiz including related questions and options by its ID.
func (s *SQLiteStore) GetQuizByID(ctx context.Context, id int64) (*Quiz, error) {
	var err error
	quizQuery := `SELECT id, title, slug, description, created_at FROM quizzes WHERE id = ?`

	quizRow := s.db.QueryRowContext(ctx, quizQuery, id)
	if quizRow.Err() != nil {
		return nil, fmt.Errorf("error iterating quizRow: %w", quizRow.Err())
	}

	var quiz Quiz
	err = quizRow.Scan(&quiz.ID, &quiz.Title, &quiz.Slug, &quiz.Description, &quiz.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: quiz %d not found", ErrQuizNotFound, id)
		}

		return nil, fmt.Errorf("error scanning quizRow: %w", err)
	}

	questions, err := s.getQuestionsByQuizID(ctx, quiz.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting questions for quiz %d: %w", quiz.ID, err)
	}

	quiz.Questions = questions

	return &quiz, nil
}

// ListQuizzes returns all quizzes including related questions and options.
func (s *SQLiteStore) ListQuizzes(ctx context.Context) ([]*Quiz, error) {
	quizQuery := `SELECT id, title, slug, description, created_at FROM quizzes`

	quizRows, quizErr := s.db.QueryContext(
		ctx,
		quizQuery,
	)
	if quizErr != nil {
		return nil, fmt.Errorf("error querying quizzes: %w", quizErr)
	}
	defer func() {
		err := quizRows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing quizRows", err)
		}
	}()
	if quizRows.Err() != nil {
		return nil, fmt.Errorf("error iterating quizRows: %w", quizRows.Err())
	}
	var quizzes []*Quiz
	for quizRows.Next() {
		quiz := &Quiz{}
		quizErr = quizRows.Scan(
			&quiz.ID,
			&quiz.Title,
			&quiz.Slug,
			&quiz.Description,
			&quiz.CreatedAt,
		)
		if quizErr != nil {
			return nil, fmt.Errorf("error scanning quizRow: %w", quizErr)
		}

		quizzes = append(quizzes, quiz)
	}

	for _, quiz := range quizzes {
		questions, err := s.getQuestionsByQuizID(ctx, quiz.ID)
		if err != nil {
			return nil, fmt.Errorf("error getting questions for quiz %d: %w", quiz.ID, err)
		}

		quiz.Questions = questions
	}

	return quizzes, nil
}

// GetQuestionByID returns a question including related options by its ID.
func (s *SQLiteStore) GetQuestionByID(ctx context.Context, id int64) (*Question, error) {
	questionQuery := `SELECT id, quiz_id, text, image_url, position FROM questions WHERE id = ?`

	questionRow := s.db.QueryRowContext(ctx, questionQuery, id)
	if questionRow.Err() != nil {
		return nil, fmt.Errorf("error iterating questionRow: %w", questionRow.Err())
	}

	question := &Question{}
	err := questionRow.Scan(&question.ID, &question.QuizID, &question.Text, &question.ImageURL, &question.Position)
	if err != nil {
		return nil, fmt.Errorf("error scanning questionRow: %w", err)
	}

	options, err := s.getOptionsByQuestionID(ctx, question.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting options for question %d: %w", question.ID, err)
	}
	question.Options = options

	return question, nil
}

// UpdateQuiz updates a quiz.
func (s *SQLiteStore) UpdateQuiz(ctx context.Context, quiz *Quiz) error {
	query := `UPDATE quizzes SET title = ?, slug = ?, description = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, quiz.Title, quiz.Slug, quiz.Description, quiz.ID)
	if err != nil {
		return fmt.Errorf("error updating quiz: %w", err)
	}

	return nil
}

// UpdateQuestion updates a question.
func (s *SQLiteStore) UpdateQuestion(ctx context.Context, question *Question) error {
	query := `UPDATE questions SET text = ?, image_url = ?, position = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, question.Text, question.ImageURL, question.Position, question.ID)
	if err != nil {
		return fmt.Errorf("error updating question: %w", err)
	}

	return nil
}

// getQuestionsByQuizID returns questions including related options for a quiz by its quizID.
func (s *SQLiteStore) getQuestionsByQuizID(ctx context.Context, quizID int64) ([]*Question, error) {
	questionQuery := `SELECT id, quiz_id, text, image_url, position FROM questions WHERE quiz_id = ?`

	questionRows, questionErr := s.db.QueryContext(ctx, questionQuery, quizID)
	if questionErr != nil {
		return nil, fmt.Errorf("error querying questions: %w", questionErr)
	}
	defer func() {
		err := questionRows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing questionRows", err)
		}
	}()

	if questionRows.Err() != nil {
		return nil, fmt.Errorf("error iterating questionRows: %w", questionRows.Err())
	}
	var questions []*Question
	for questionRows.Next() {
		question := &Question{}
		questionErr = questionRows.Scan(
			&question.ID,
			&question.QuizID,
			&question.Text,
			&question.ImageURL,
			&question.Position,
		)
		if questionErr != nil {
			return nil, fmt.Errorf("error scanning questionRow: %w", questionErr)
		}

		options, err := s.getOptionsByQuestionID(ctx, question.ID)
		if err != nil {
			return nil, fmt.Errorf(
				"error getting options for question %d: %w",
				question.ID,
				err,
			)
		}
		question.Options = options

		questions = append(questions, question)
	}

	return questions, nil
}

// getOptionsByQuestionID returns options for a question by its questionID.
func (s *SQLiteStore) getOptionsByQuestionID(
	ctx context.Context,
	questionID int64,
) ([]*Option, error) {
	optionQuery := `SELECT id, question_id, text, is_correct FROM options WHERE question_id = ?`

	optionRows, optionErr := s.db.QueryContext(ctx, optionQuery, questionID)
	if optionErr != nil {
		return nil, fmt.Errorf("error querying options: %w", optionErr)
	}
	defer func() {
		err := optionRows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing optionRows", err)
		}
	}()

	if optionRows.Err() != nil {
		return nil, fmt.Errorf("error iterating optionRows: %w", optionRows.Err())
	}
	var options []*Option
	for optionRows.Next() {
		option := &Option{}
		optionErr = optionRows.Scan(
			&option.ID,
			&option.QuestionID,
			&option.Text,
			&option.Correct,
		)
		if optionErr != nil {
			return nil, fmt.Errorf("error scanning optionRow: %w", optionErr)
		}
		options = append(options, option)
	}

	return options, nil
}
