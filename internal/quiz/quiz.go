// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/starquake/topbanana/internal/logging"
)

// ErrConvertingValueIntoTimestamp is returned when a value cannot be converted into a Timestamp.
var ErrConvertingValueIntoTimestamp = errors.New("cannot convert value into Timestamp")

// Timestamp is a timestamp with millisecond precision. Used for SQLite type conversion.
//
//nolint:recvcheck // Mixing pointer receivers and value receivers is needed here because we are implementing sql.Scanner and driver.Valuer.
type Timestamp time.Time

// Scan converts a value to a Timestamp.
// Currently, only int64 values are supported.
func (t *Timestamp) Scan(value any) error {
	if value == nil {
		*t = Timestamp(time.Time{})

		return nil
	}

	ms, ok := value.(int64)
	if !ok {
		return fmt.Errorf("%w: %T", ErrConvertingValueIntoTimestamp, value)
	}

	*t = Timestamp(time.UnixMilli(ms))

	return nil
}

// Value converts a Timestamp to a value suitable for database storage.
func (t Timestamp) Value() (driver.Value, error) {
	return time.Time(t).UnixMilli(), nil
}

// Quiz represents a quiz.
type Quiz struct {
	ID          int64
	Title       string
	Slug        string
	Description string
	CreatedAt   time.Time
	Questions   []*Question
}

// Valid checks if the quiz is valid.
func (q *Quiz) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	if q.Title == "" {
		problems["title"] = "Title is required"
	}
	if q.Slug == "" {
		problems["slug"] = "Slug is required"
	}
	if q.Description == "" {
		problems["description"] = "Description is required"
	}

	return problems
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

// Valid checks if the question is valid.
func (q *Question) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	if q.Text == "" {
		problems["text"] = "Text is required"
	}
	if len(q.Options) == 0 {
		problems["options"] = "Options are required"
	}

	return problems
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
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, quiz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, quiz *Quiz) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, question *Question) error
	// CreateQuestion creates a question.
	CreateQuestion(ctx context.Context, qs *Question) error
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
	ErrQuestionNotFound = errors.New("question not found")
)

// NewSQLiteStore creates a new SQLiteStore.
func NewSQLiteStore(db *sql.DB, logger *logging.Logger) *SQLiteStore {
	return &SQLiteStore{db, logger}
}

// GetQuizByID returns a quiz including related questions and options by its ID.
func (s *SQLiteStore) GetQuizByID(ctx context.Context, quizID int64) (*Quiz, error) {
	var err error
	quizQuery := `SELECT id, title, slug, description, created_at FROM quizzes WHERE id = ?`

	quizRow := s.db.QueryRowContext(ctx, quizQuery, quizID)
	if quizRow.Err() != nil {
		return nil, fmt.Errorf("error iterating quizRow: %w", quizRow.Err())
	}

	var id int64
	var title, slug, description string
	var createdAt Timestamp

	err = quizRow.Scan(&id, &title, &slug, &description, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: quiz %d not found", ErrQuizNotFound, quizID)
		}

		return nil, fmt.Errorf("error scanning quizRow: %w", err)
	}

	quiz := Quiz{
		ID:          id,
		Title:       title,
		Slug:        slug,
		Description: description,
		CreatedAt:   time.Time(createdAt),
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
			s.logger.Error(ctx, "error closing quizRows", logging.ErrAttr(err))
		}
	}()
	if quizRows.Err() != nil {
		return nil, fmt.Errorf("error iterating quizRows: %w", quizRows.Err())
	}
	var quizzes []*Quiz

	var id int64
	var title, slug, description string
	var createdAt Timestamp

	for quizRows.Next() {
		quizErr = quizRows.Scan(
			&id, &title, &slug, &description, &createdAt,
		)
		quiz := &Quiz{
			ID:          id,
			Title:       title,
			Slug:        slug,
			Description: description,
			CreatedAt:   time.Time(createdAt),
		}
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
func (s *SQLiteStore) GetQuestionByID(ctx context.Context, questionID int64) (*Question, error) {
	questionQuery := `SELECT id, quiz_id, text, image_url, position FROM questions WHERE id = ?`

	questionRow := s.db.QueryRowContext(ctx, questionQuery, questionID)
	if questionRow.Err() != nil {
		return nil, fmt.Errorf("error iterating questionRow: %w", questionRow.Err())
	}

	question := &Question{}
	err := questionRow.Scan(&question.ID, &question.QuizID, &question.Text, &question.ImageURL, &question.Position)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: quiz %d not found", ErrQuestionNotFound, questionID)
		}

		return nil, fmt.Errorf("error scanning questionRow: %w", err)
	}

	options, err := s.getOptionsByQuestionID(ctx, question.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting options for question %d: %w", question.ID, err)
	}
	question.Options = options

	return question, nil
}

// CreateQuiz creates a quiz.
func (s *SQLiteStore) CreateQuiz(ctx context.Context, quiz *Quiz) error {
	query := `INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`
	createdAt := Timestamp(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, query, quiz.Title, quiz.Slug, quiz.Description, createdAt)
	if err != nil {
		return fmt.Errorf("error creating quiz: %w", err)
	}
	resultID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting last insert ID: %w", err)
	}
	quiz.ID = resultID

	return nil
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

// CreateQuestion creates a question and its options and saves them to the database.
func (s *SQLiteStore) CreateQuestion(ctx context.Context, qs *Question) error {
	query := `INSERT INTO questions (quiz_id, text, image_url, position) VALUES (?, ?, ?, ?)`
	optionQuery := `INSERT INTO options (question_id, text, is_correct) VALUES (?, ?, ?)`

	var result sql.Result
	var err error
	result, err = s.db.ExecContext(ctx, query, qs.QuizID, qs.Text, qs.ImageURL, qs.Position)
	if err != nil {
		return fmt.Errorf("error creating question: %w", err)
	}
	resultID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting last insert ID: %w", err)
	}
	qs.ID = resultID

	for _, option := range qs.Options {
		_, err = s.db.ExecContext(ctx, optionQuery, qs.ID, option.Text, option.Correct)
		if err != nil {
			return fmt.Errorf("error creating option: %w", err)
		}
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
			s.logger.Error(ctx, "error closing questionRows", logging.ErrAttr(err))
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
			s.logger.Error(ctx, "error closing optionRows", logging.ErrAttr(err))
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
