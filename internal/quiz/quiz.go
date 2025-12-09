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
	"github.com/starquake/topbanana/internal/must"
)

const (
	createOptionSQL             = `INSERT INTO options (question_id, text, is_correct) VALUES (?, ?, ?)`
	createQuestionSQL           = `INSERT INTO questions (quiz_id, text, image_url, position) VALUES (?, ?, ?, ?)`
	createQuizSQL               = `INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`
	deleteOptionSQL             = `DELETE FROM options WHERE id = ?`
	deleteQuestionSQL           = `DELETE FROM questions WHERE id = ?`
	getOptionIDsByQuestionIDSQL = `SELECT id FROM options WHERE question_id = ?`
	getOptionsByQuestionIDSQL   = `SELECT id, question_id, text, is_correct FROM options WHERE question_id = ?`
	getQuestionByIDSQL          = `SELECT id, quiz_id, text, image_url, position FROM questions WHERE id = ?`
	getQuestionIDsByQuizIDSQL   = `SELECT id FROM questions WHERE quiz_id = ?`
	getQuestionsByQuizIDSQL     = `SELECT id, quiz_id, text, image_url, position FROM questions WHERE quiz_id = ?`
	getQuizByIDSQL              = listQuizzesSQL + ` WHERE id = ?`
	listQuizzesSQL              = `SELECT id, title, slug, description, created_at FROM quizzes`
	updateOptionSQL             = `UPDATE options SET text = ?, is_correct = ? WHERE id = ?`
	updateQuestionSQL           = `UPDATE questions SET text = ?, image_url = ?, position = ? WHERE id = ?`
	updateQuizSQL               = `UPDATE quizzes SET title = ?, slug = ?, description = ? WHERE id = ?`
)

var (
	// ErrConvertingValueIntoTimestamp is returned when a value cannot be converted into a Timestamp.
	ErrConvertingValueIntoTimestamp = errors.New("cannot convert value into Timestamp")
	// ErrQuizNotFound is returned when a quiz is not found.
	ErrQuizNotFound = errors.New("quiz not found")
	// ErrQuestionNotFound is returned when a question is not found.
	ErrQuestionNotFound = errors.New("question not found")
)

// Timestamp is a timestamp with millisecond precision. Used for SQLite type conversion.
//
//nolint:recvcheck // Mixing pointer receivers and value receivers is needed here because we are implementing sql.Scanner and driver.Valuer.
type Timestamp time.Time

// Scan converts a value to a Timestamp in UTC.
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

	*t = Timestamp(time.UnixMilli(ms).UTC())

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

// Valid checks if the quiz, its questions, and its options are valid.
func (q *Quiz) Valid(ctx context.Context) map[string]string {
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
	for qsIndex, question := range q.Questions {
		if qsProblems := question.Valid(ctx); len(qsProblems) > 0 {
			for qsProblemKey, v := range qsProblems {
				problems[fmt.Sprintf("questions[%d][%s]", qsIndex, qsProblemKey)] = v
			}
		}
		for oIndex, option := range question.Options {
			if oProblems := option.Valid(ctx); len(oProblems) > 0 {
				for oProblemKey, v := range oProblems {
					problems[fmt.Sprintf("questions[%d].options[%d][%s]", qsIndex, oIndex, oProblemKey)] = v
				}
			}
		}
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

// Valid checks if the question and its options are valid.
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

// Valid checks if the option is valid.
func (o *Option) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	if o.Text == "" {
		problems["text"] = "Text is required"
	}

	return problems
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

// NewSQLiteStore creates a new SQLiteStore.
func NewSQLiteStore(db *sql.DB, logger *logging.Logger) *SQLiteStore {
	return &SQLiteStore{db, logger}
}

// GetQuizByID returns a quiz including related questions and options by its ID.
// Returns ErrQuizNotFound if the quiz is not found.
func (s *SQLiteStore) GetQuizByID(ctx context.Context, quizID int64) (*Quiz, error) {
	var err error

	quizRow := s.db.QueryRowContext(ctx, getQuizByIDSQL, quizID)
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
	quizzes, funcErr := func() ([]*Quiz, error) {
		rows, err := s.db.QueryContext(ctx, listQuizzesSQL)
		if err != nil {
			return nil, fmt.Errorf("error querying out: %w", err)
		}
		defer func() {
			// Close the rows to free up resources. It must not return an error.
			// It will not return an error because we checked for errors before (quizRows.Err()).
			must.OK(rows.Close())
		}()

		var out []*Quiz
		for rows.Next() {
			qz := &Quiz{}
			if err = rows.Scan(&qz.ID, &qz.Title, &qz.Slug, &qz.Description, (*Timestamp)(&qz.CreatedAt)); err != nil {
				return nil, fmt.Errorf("error scanning quizRow: %w", err)
			}
			out = append(out, qz)
		}
		if err = rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating quizRows: %w", err)
		}

		return out, nil
	}()
	if funcErr != nil {
		return nil, fmt.Errorf("error listing quizzes: %w", funcErr)
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
	questionRow := s.db.QueryRowContext(ctx, getQuestionByIDSQL, questionID)
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
	// TODO: Use a transaction here. Also updates the tests.
	quizResult, err := s.db.ExecContext(ctx, createQuizSQL,
		quiz.Title, quiz.Slug, quiz.Description, Timestamp(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("error creating quiz: %w", err)
	}
	resultID, err := quizResult.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting last insert ID for quiz: %w", err)
	}
	quiz.ID = resultID

	for _, question := range quiz.Questions {
		questionResult, err := s.db.ExecContext(
			ctx, createQuestionSQL,
			quiz.ID, question.Text, question.ImageURL, question.Position,
		)
		if err != nil {
			return fmt.Errorf("error creating question: %w", err)
		}
		questionID, err := questionResult.LastInsertId()
		if err != nil {
			return fmt.Errorf("error getting last insert ID for question: %w", err)
		}
		question.ID = questionID
		question.QuizID = quiz.ID
	}
	for _, question := range quiz.Questions {
		for _, option := range question.Options {
			optionResult, err := s.db.ExecContext(ctx, createOptionSQL, question.ID, option.Text, option.Correct)
			if err != nil {
				return fmt.Errorf("error creating option: %w", err)
			}
			optionID, err := optionResult.LastInsertId()
			if err != nil {
				return fmt.Errorf("error getting last insert ID for option: %w", err)
			}
			option.ID = optionID
			option.QuestionID = question.ID
		}
	}

	return nil
}

// UpdateQuiz updates a quiz, questions, and their options.
func (s *SQLiteStore) UpdateQuiz(ctx context.Context, quiz *Quiz) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}
	defer func() {
		err = tx.Rollback()
		if err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.Error(ctx, "error rolling back transaction", logging.ErrAttr(err))
		}
	}()

	// Update Quiz
	res, err := tx.ExecContext(ctx, updateQuizSQL, quiz.Title, quiz.Slug, quiz.Description, quiz.ID)
	if err != nil {
		return fmt.Errorf("error updating quiz: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("no rows affected when updating quiz: %d", quiz.ID)
	}

	// Handle Questions (Create, Update, Delete)
	err = s.handleQuestionsInTx(ctx, tx, quiz)
	if err != nil {
		return fmt.Errorf("error handling questions in transaction: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

// CreateQuestion creates a question and its options and saves them to the database.
func (s *SQLiteStore) CreateQuestion(ctx context.Context, qs *Question) error {
	var result sql.Result
	var err error
	result, err = s.db.ExecContext(ctx, createQuestionSQL, qs.QuizID, qs.Text, qs.ImageURL, qs.Position)
	if err != nil {
		return fmt.Errorf("error creating question: %w", err)
	}
	resultID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting last insert ID: %w", err)
	}
	qs.ID = resultID

	for _, option := range qs.Options {
		_, err = s.db.ExecContext(ctx, createOptionSQL, qs.ID, option.Text, option.Correct)
		if err != nil {
			return fmt.Errorf("error creating option: %w", err)
		}
	}

	return nil
}

// UpdateQuestion updates a question.
func (s *SQLiteStore) UpdateQuestion(ctx context.Context, question *Question) error {
	_, err := s.db.ExecContext(ctx, updateQuestionSQL, question.Text, question.ImageURL, question.Position, question.ID)
	if err != nil {
		return fmt.Errorf("error updating question: %w", err)
	}

	return nil
}

func (s *SQLiteStore) handleQuestionsInTx(ctx context.Context, tx *sql.Tx, quiz *Quiz) error {
	existingQIDs, err := s.getQuestionIDsInTx(ctx, tx, quiz.ID)
	if err != nil {
		return fmt.Errorf("error getting questionIDs: %w", err)
	}

	incomingQIDs := make(map[int64]bool)
	for _, q := range quiz.Questions {
		q.QuizID = quiz.ID // Ensure linkage

		if q.ID == 0 {
			// CREATE
			if err = s.createQuestionInTx(ctx, tx, q); err != nil {
				return fmt.Errorf("error creating new question (text: %q): %w", q.Text, err)
			}
		} else {
			// UPDATE
			if err = s.updateQuestionInTx(ctx, tx, q); err != nil {
				return fmt.Errorf("error updating question %d: %w", q.ID, err)
			}
			incomingQIDs[q.ID] = true
		}
		// Handle Options for this question (regardless of create or update)
		if err = s.handleOptionsInTx(ctx, tx, q); err != nil {
			return fmt.Errorf("error handling options for question %d: %w", q.ID, err)
		}
	}

	// DELETE missing questions
	for _, id := range existingQIDs {
		if !incomingQIDs[id] {
			if _, err = tx.ExecContext(ctx, deleteQuestionSQL, id); err != nil {
				return fmt.Errorf("error deleting question %d: %w", id, err)
			}
		}
	}

	return nil
}

func (s *SQLiteStore) handleOptionsInTx(ctx context.Context, tx *sql.Tx, q *Question) error {
	existingOIDs, err := s.getOptionIDsInTx(ctx, tx, q.ID)
	if err != nil {
		return err
	}

	incomingOIDs := make(map[int64]bool)
	for _, o := range q.Options {
		o.QuestionID = q.ID // Ensure linkage

		if o.ID == 0 {
			// CREATE Option
			res, err := tx.ExecContext(ctx, createOptionSQL, q.ID, o.Text, o.Correct)
			if err != nil {
				return fmt.Errorf("error creating option: %w", err)
			}
			o.ID, err = res.LastInsertId()
			if err != nil {
				return fmt.Errorf("error getting option ID: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, updateOptionSQL, o.Text, o.Correct, o.ID); err != nil {
				return fmt.Errorf("error updating option %d: %w", o.ID, err)
			}
			incomingOIDs[o.ID] = true
		}
	}

	// DELETE missing options
	for _, id := range existingOIDs {
		if !incomingOIDs[id] {
			if _, err := tx.ExecContext(ctx, deleteOptionSQL, id); err != nil {
				return fmt.Errorf("error deleting option %d: %w", id, err)
			}
		}
	}

	return nil
}

func (*SQLiteStore) getQuestionIDsInTx(ctx context.Context, tx *sql.Tx, quizID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, getQuestionIDsByQuizIDSQL, quizID)
	if err != nil {
		return nil, fmt.Errorf("error querying questionIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("error scanning questionIDs: %w", err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating questionIDs: %w", err)
	}

	return ids, nil
}

func (*SQLiteStore) getOptionIDsInTx(ctx context.Context, tx *sql.Tx, questionID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, getOptionIDsByQuestionIDSQL, questionID)
	if err != nil {
		return nil, fmt.Errorf("error querying optionIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("error scanning optionIDs: %w", err)
		}
		ids = append(ids, id)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating optionIDs: %w", err)
	}

	return ids, nil
}

func (*SQLiteStore) createQuestionInTx(ctx context.Context, tx *sql.Tx, q *Question) error {
	res, err := tx.ExecContext(ctx, createQuestionSQL, q.QuizID, q.Text, q.ImageURL, q.Position)
	if err != nil {
		return fmt.Errorf("error creating question: %w", err)
	}
	q.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting question ID: %w", err)
	}

	return nil
}

func (*SQLiteStore) updateQuestionInTx(ctx context.Context, tx *sql.Tx, q *Question) error {
	_, err := tx.ExecContext(ctx, updateQuestionSQL, q.Text, q.ImageURL, q.Position, q.ID)
	if err != nil {
		return fmt.Errorf("error updating question: %w", err)
	}

	return nil
}

// getQuestionsByQuizIDSQL returns questions including related options for a quiz by its quizID.
func (s *SQLiteStore) getQuestionsByQuizID(ctx context.Context, quizID int64) ([]*Question, error) {
	questionRows, err := s.db.QueryContext(ctx, getQuestionsByQuizIDSQL, quizID)
	if err != nil {
		return nil, fmt.Errorf("error querying questions: %w", err)
	}
	defer func() {
		err = questionRows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing questionRows", logging.ErrAttr(err))
		}
	}()

	var questions []*Question
	for questionRows.Next() {
		qs := &Question{}
		if err = questionRows.Scan(&qs.ID, &qs.QuizID, &qs.Text, &qs.ImageURL, &qs.Position); err != nil {
			return nil, fmt.Errorf("error scanning questionRow: %w", err)
		}
		questions = append(questions, qs)
	}
	if questionRows.Err() != nil {
		return nil, fmt.Errorf("error iterating questionRows: %w", questionRows.Err())
	}

	for _, question := range questions {
		options, err := s.getOptionsByQuestionID(ctx, question.ID)
		if err != nil {
			return nil, fmt.Errorf(
				"error getting options for question %d: %w",
				question.ID,
				err,
			)
		}
		question.Options = options
	}

	return questions, nil
}

// getOptionsByQuestionID returns options for a question by its questionID.
func (s *SQLiteStore) getOptionsByQuestionID(
	ctx context.Context,
	questionID int64,
) ([]*Option, error) {
	optionRows, optionErr := s.db.QueryContext(ctx, getOptionsByQuestionIDSQL, questionID)
	if optionErr != nil {
		return nil, fmt.Errorf("error querying options: %w", optionErr)
	}
	defer func() {
		err := optionRows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing optionRows", logging.ErrAttr(err))
		}
	}()

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
	if optionRows.Err() != nil {
		return nil, fmt.Errorf("error iterating optionRows: %w", optionRows.Err())
	}

	return options, nil
}
