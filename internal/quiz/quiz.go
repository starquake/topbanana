// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"time"
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
	// ErrUpdatingQuizNoRowsAffected is returned when no rows are affected when updating a quiz.
	ErrUpdatingQuizNoRowsAffected = errors.New("no rows affected when updating quiz")
	// ErrUpdatingQuestionNoRowsAffected is returned when no rows are affected when updating a question.
	ErrUpdatingQuestionNoRowsAffected = errors.New("no rows affected when updating question")
	// ErrUpdatingOptionNoRowsAffected is returned when no rows are affected when updating a option.
	ErrUpdatingOptionNoRowsAffected = errors.New("no rows affected when updating option")
	// ErrCannotUpdateQuizWithIDZero is returned when trying to update a quiz with ID 0.
	ErrCannotUpdateQuizWithIDZero = errors.New("cannot update quiz with ID 0")
	// ErrCannotUpdateQuestionWithIDZero is returned when trying to update a question with ID 0.
	ErrCannotUpdateQuestionWithIDZero = errors.New("cannot update question with ID 0")
)

// Timestamp is a timestamp with millisecond precision. Used for SQLite type conversion.
// TODO: Move to shared package
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
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	// GetQuizByID returns a quiz including related questions and options by its ID.
	// Returns ErrQuizNotFound if the quiz is not found.
	GetQuizByID(ctx context.Context, id int64) (*Quiz, error)
	// GetQuestionByID returns a question with options, by its question ID.
	GetQuestionByID(ctx context.Context, id int64) (*Question, error)
	// ListQuizzes returns all quizzes.
	ListQuizzes(ctx context.Context) ([]*Quiz, error)
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, quiz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, quiz *Quiz) error
	// CreateQuestion creates a question.
	CreateQuestion(ctx context.Context, qs *Question) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, question *Question) error
}

// SQLiteStore is a store for quizzes in SQLite.
type SQLiteStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewSQLiteStore creates a new SQLiteStore.
func NewSQLiteStore(db *sql.DB, logger *slog.Logger) *SQLiteStore {
	return &SQLiteStore{db, logger}
}

// GetQuizByID returns a quiz including related questions and options by its ID.
// Returns ErrQuizNotFound if the quiz is not found.
func (s *SQLiteStore) GetQuizByID(ctx context.Context, quizID int64) (*Quiz, error) {
	var err error

	row := s.db.QueryRowContext(ctx, getQuizByIDSQL, quizID)
	if row.Err() != nil {
		return nil, fmt.Errorf("error iterating row: %w", row.Err())
	}

	qz := &Quiz{}
	if err = row.Scan(&qz.ID, &qz.Title, &qz.Slug, &qz.Description, (*Timestamp)(&qz.CreatedAt)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: quiz %d not found", ErrQuizNotFound, quizID)
		}

		return nil, fmt.Errorf("error scanning row: %w", err)
	}

	questions, err := s.GetQuestionsByQuizID(ctx, qz.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting questions for quiz %d: %w", qz.ID, err)
	}

	qz.Questions = questions

	return qz, nil
}

// Ping returns the status of the database connection.
func (s *SQLiteStore) Ping(ctx context.Context) error {
	err := s.db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("error pinging database: %w", err)
	}

	return nil
}

// ListQuizzes returns all quizzes including related questions and options.
func (s *SQLiteStore) ListQuizzes(ctx context.Context) ([]*Quiz, error) {
	quizzes, funcErr := s.fetchQuizzes(ctx)
	if funcErr != nil {
		return nil, fmt.Errorf("error listing quizzes: %w", funcErr)
	}

	for _, qz := range quizzes {
		questions, err := s.GetQuestionsByQuizID(ctx, qz.ID)
		if err != nil {
			return nil, fmt.Errorf("error getting questions for qz %d: %w", qz.ID, err)
		}

		qz.Questions = questions
	}

	return quizzes, nil
}

// GetQuestionByID returns a question including related options by its ID.
func (s *SQLiteStore) GetQuestionByID(ctx context.Context, questionID int64) (*Question, error) {
	row := s.db.QueryRowContext(ctx, getQuestionByIDSQL, questionID)
	if row.Err() != nil {
		return nil, fmt.Errorf("error iterating row: %w", row.Err())
	}

	question := &Question{}
	err := row.Scan(&question.ID, &question.QuizID, &question.Text, &question.ImageURL, &question.Position)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: quiz %d not found", ErrQuestionNotFound, questionID)
		}

		return nil, fmt.Errorf("error scanning row: %w", err)
	}

	options, err := s.GetOptionsByQuestionID(ctx, question.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting options for question %d: %w", question.ID, err)
	}
	question.Options = options

	return question, nil
}

// CreateQuiz creates a quiz.
func (s *SQLiteStore) CreateQuiz(ctx context.Context, qz *Quiz) error {
	qz.ID = 0

	return withTx(ctx, s, func(tx *sql.Tx) error {
		if err := upsertQuizInTx(ctx, tx, qz); err != nil {
			return fmt.Errorf("error upserting quiz: %w", err)
		}

		return nil
	})
}

// UpdateQuiz updates a quiz, questions, and their options.
func (s *SQLiteStore) UpdateQuiz(ctx context.Context, qz *Quiz) error {
	if qz.ID == 0 {
		return ErrCannotUpdateQuizWithIDZero
	}

	return withTx(ctx, s, func(tx *sql.Tx) error {
		if err := upsertQuizInTx(ctx, tx, qz); err != nil {
			return fmt.Errorf("error updating quiz: %w", err)
		}

		return nil
	})
}

// CreateQuestion creates a question and its options and saves them to the database.
func (s *SQLiteStore) CreateQuestion(ctx context.Context, qs *Question) error {
	qs.ID = 0

	return withTx(ctx, s, func(tx *sql.Tx) error {
		if err := upsertQuestionInTx(ctx, tx, qs); err != nil {
			return fmt.Errorf("error upserting question: %w", err)
		}

		return nil
	})
}

// UpdateQuestion updates a question.
func (s *SQLiteStore) UpdateQuestion(ctx context.Context, qs *Question) error {
	if qs.ID == 0 {
		return ErrCannotUpdateQuestionWithIDZero
	}

	return withTx(ctx, s, func(tx *sql.Tx) error {
		if err := upsertQuestionInTx(ctx, tx, qs); err != nil {
			return fmt.Errorf("error updating question: %w", err)
		}

		return nil
	})
}

// GetQuestionsByQuizID returns questions including related options for a quiz by its quizID.
func (s *SQLiteStore) GetQuestionsByQuizID(ctx context.Context, quizID int64) ([]*Question, error) {
	var err error
	var rows *sql.Rows
	rows, err = s.db.QueryContext(ctx, getQuestionsByQuizIDSQL, quizID)
	if err != nil {
		return nil, fmt.Errorf("error querying questions: %w", err)
	}
	defer func() {
		// Close the rows to free up resources. It must not return an error.
		// It will not return an error because we checked for errors before (rows.Err()).
		_ = rows.Close()
	}()

	var questions []*Question
	for rows.Next() {
		qs := &Question{}
		if err = rows.Scan(&qs.ID, &qs.QuizID, &qs.Text, &qs.ImageURL, &qs.Position); err != nil {
			return nil, fmt.Errorf("error scanning questionRow: %w", err)
		}
		questions = append(questions, qs)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("error iterating rows: %w", rows.Err())
	}

	for _, question := range questions {
		options, err := s.GetOptionsByQuestionID(ctx, question.ID)
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

// GetOptionsByQuestionID returns options for a question by its questionID.
func (s *SQLiteStore) GetOptionsByQuestionID(ctx context.Context, questionID int64) ([]*Option, error) {
	rows, optionErr := s.db.QueryContext(ctx, getOptionsByQuestionIDSQL, questionID)
	if optionErr != nil {
		return nil, fmt.Errorf("error querying options: %w", optionErr)
	}
	defer func() {
		// Close the rows to free up resources. It must not return an error.
		// It will not return an error because we checked for errors before (rows.Err()).
		_ = rows.Close()
	}()

	var options []*Option
	for rows.Next() {
		option := &Option{}
		optionErr = rows.Scan(
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
	if rows.Err() != nil {
		return nil, fmt.Errorf("error iterating rows: %w", rows.Err())
	}

	return options, nil
}

func withTx(ctx context.Context, s *SQLiteStore, fn func(tx *sql.Tx) error) error {
	var txn *sql.Tx
	var err error
	if txn, err = s.db.BeginTx(ctx, nil); err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}
	defer func() {
		err = txn.Rollback()
		if err == nil {
			s.logger.InfoContext(ctx, "rollback transaction successful")
		}
		if err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.ErrorContext(ctx, "error rolling back transaction", slog.Any("err", err))
		}
	}()
	err = fn(txn)
	if err != nil {
		return fmt.Errorf("error executing transaction: %w", err)
	}

	err = txn.Commit()
	if err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

func (s *SQLiteStore) fetchQuizzes(ctx context.Context) ([]*Quiz, error) {
	rows, err := s.db.QueryContext(ctx, listQuizzesSQL)
	if err != nil {
		return nil, fmt.Errorf("error querying quizzes: %w", err)
	}
	defer func() {
		// Close the rows to free up resources. It must not return an error.
		// It will not return an error because we checked for errors before (rows.Err()).
		_ = rows.Close()
	}()

	var quizzes []*Quiz
	for rows.Next() {
		qz := &Quiz{}
		if err = rows.Scan(&qz.ID, &qz.Title, &qz.Slug, &qz.Description, (*Timestamp)(&qz.CreatedAt)); err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}
		quizzes = append(quizzes, qz)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return quizzes, nil
}

func upsertQuizInTx(ctx context.Context, tx *sql.Tx, qz *Quiz) error {
	if qz.ID == 0 {
		// CREATE
		for _, question := range qz.Questions {
			question.ID = 0
		}
		if err := createQuizInTx(ctx, tx, qz); err != nil {
			return fmt.Errorf("error creating new quiz (title: %q): %w", qz.Title, err)
		}
	} else {
		// UPDATE
		if err := updateQuizInTx(ctx, tx, qz); err != nil {
			return fmt.Errorf("error updating quiz %d: %w", qz.ID, err)
		}
	}

	// Handle Questions for this quiz (regardless of create or update)
	if err := handleQuestionsInTx(ctx, tx, qz.Questions, qz.ID); err != nil {
		return fmt.Errorf("error handling questions for quiz %d: %w", qz.ID, err)
	}

	return nil
}

func createQuizInTx(ctx context.Context, tx *sql.Tx, qz *Quiz) error {
	qz.CreatedAt = time.Now().UTC()
	res, err := tx.ExecContext(ctx, createQuizSQL, qz.Title, qz.Slug, qz.Description, Timestamp(qz.CreatedAt))
	if err != nil {
		return fmt.Errorf("error creating quiz: %w", err)
	}
	qz.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting last insert ID for quiz: %w", err)
	}

	return nil
}

func updateQuizInTx(ctx context.Context, tx *sql.Tx, qz *Quiz) error {
	res, err := tx.ExecContext(ctx, updateQuizSQL, qz.Title, qz.Slug, qz.Description, qz.ID)
	if err != nil {
		return fmt.Errorf("error updating quiz: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: quizID %d", ErrUpdatingQuizNoRowsAffected, qz.ID)
	}

	return nil
}

func getQuestionIDsInTx(ctx context.Context, tx *sql.Tx, quizID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, getQuestionIDsByQuizIDSQL, quizID)
	if err != nil {
		return nil, fmt.Errorf("error querying questionIDs: %w", err)
	}
	defer func() {
		// Close the rows to free up resources. It must not return an error.
		// It will not return an error because we checked for errors before (rows.Err()).
		_ = rows.Close()
	}()

	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("error scanning questionIDs: %w", err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating questionIDs: %w", err)
	}

	return ids, nil
}

func handleQuestionsInTx(ctx context.Context, tx *sql.Tx, questions []*Question, quizID int64) error {
	existingIDs, err := getQuestionIDsInTx(ctx, tx, quizID)
	if err != nil {
		return fmt.Errorf("error getting questionIDs: %w", err)
	}

	// UPSERT
	incomingIDs := make(map[int64]bool)
	for _, qs := range questions {
		qs.QuizID = quizID // Ensure linkage

		// Track incoming IDs for updates
		if qs.ID != 0 {
			incomingIDs[qs.ID] = true
		}

		if err = upsertQuestionInTx(ctx, tx, qs); err != nil {
			return fmt.Errorf("error upserting question %d: %w", qs.ID, err)
		}
	}

	// DELETE
	deleteIDs := make([]int64, 0, len(existingIDs))
	for _, id := range existingIDs {
		if !incomingIDs[id] {
			deleteIDs = append(deleteIDs, id)
		}
	}

	return deleteQuestionsInTx(ctx, tx, deleteIDs)
}

func upsertQuestionInTx(ctx context.Context, tx *sql.Tx, qs *Question) error {
	if qs.ID == 0 {
		// CREATE
		for _, option := range qs.Options {
			option.ID = 0
		}
		if err := createQuestionInTx(ctx, tx, qs); err != nil {
			return fmt.Errorf("error creating new question (text: %q): %w", qs.Text, err)
		}
	} else {
		// UPDATE
		if err := updateQuestionInTx(ctx, tx, qs); err != nil {
			return fmt.Errorf("error updating question %d: %w", qs.ID, err)
		}
	}

	// Handle Options for this question (regardless of create or update)
	if err := handleOptionsInTx(ctx, tx, qs.Options, qs.ID); err != nil {
		return fmt.Errorf("error handling options for question %d: %w", qs.ID, err)
	}

	return nil
}

func createQuestionInTx(ctx context.Context, tx *sql.Tx, qs *Question) error {
	res, err := tx.ExecContext(ctx, createQuestionSQL, qs.QuizID, qs.Text, qs.ImageURL, qs.Position)
	if err != nil {
		return fmt.Errorf("error creating question: %w", err)
	}
	qs.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting question ID: %w", err)
	}

	return nil
}

func updateQuestionInTx(ctx context.Context, tx *sql.Tx, qs *Question) error {
	res, err := tx.ExecContext(ctx, updateQuestionSQL, qs.Text, qs.ImageURL, qs.Position, qs.ID)
	if err != nil {
		return fmt.Errorf("error updating question: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: questionID %d", ErrUpdatingQuestionNoRowsAffected, qs.ID)
	}

	return nil
}

func deleteQuestionsInTx(ctx context.Context, tx *sql.Tx, deleteIDs []int64) error {
	for _, id := range deleteIDs {
		if _, err := tx.ExecContext(ctx, deleteQuestionSQL, id); err != nil {
			return fmt.Errorf("error deleting question %d: %w", id, err)
		}
	}

	return nil
}

func getOptionIDsInTx(ctx context.Context, tx *sql.Tx, questionID int64) ([]int64, error) {
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

func handleOptionsInTx(ctx context.Context, tx *sql.Tx, options []*Option, questionID int64) error {
	var existingOIDs []int64
	var err error
	if existingOIDs, err = getOptionIDsInTx(ctx, tx, questionID); err != nil {
		return fmt.Errorf("error getting options: %w", err)
	}

	// UPSERT
	incomingOIDs := make(map[int64]bool)
	for _, o := range options {
		o.QuestionID = questionID // Ensure linkage

		if o.ID != 0 {
			incomingOIDs[o.ID] = true
		}
		if err = upsertOptionInTx(ctx, tx, o); err != nil {
			return fmt.Errorf("error upserting option %d: %w", o.ID, err)
		}
	}

	// DELETE
	deleteOIDs := make([]int64, 0, len(existingOIDs))
	for _, oid := range existingOIDs {
		if !incomingOIDs[oid] {
			deleteOIDs = append(deleteOIDs, oid)
		}
	}

	err = deleteOptionsInTx(ctx, tx, deleteOIDs)
	if err != nil {
		return fmt.Errorf("error deleting options: %w", err)
	}

	return nil
}

func upsertOptionInTx(ctx context.Context, tx *sql.Tx, o *Option) error {
	if o.ID == 0 {
		// CREATE Option
		err := createOptionInTx(ctx, tx, o)
		if err != nil {
			return fmt.Errorf("error creating new option (text: %q): %w", o.Text, err)
		}
	} else {
		// UPDATE Option
		err := updateOptionInTx(ctx, tx, o)
		if err != nil {
			return fmt.Errorf("error updating option %d: %w", o.ID, err)
		}
	}

	return nil
}

func createOptionInTx(ctx context.Context, tx *sql.Tx, o *Option) error {
	res, err := tx.ExecContext(ctx, createOptionSQL, o.QuestionID, o.Text, o.Correct)
	if err != nil {
		return fmt.Errorf("error creating option: %w", err)
	}
	o.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("error getting option ID: %w", err)
	}

	return nil
}

func updateOptionInTx(ctx context.Context, tx *sql.Tx, o *Option) error {
	res, err := tx.ExecContext(ctx, updateOptionSQL, o.Text, o.Correct, o.ID)
	if err != nil {
		return fmt.Errorf("error updating option %d: %w", o.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: optionID %d", ErrUpdatingOptionNoRowsAffected, o.ID)
	}

	return nil
}

func deleteOptionsInTx(ctx context.Context, tx *sql.Tx, deleteIDs []int64) error {
	for _, id := range deleteIDs {
		if _, err := tx.ExecContext(ctx, deleteOptionSQL, id); err != nil {
			return fmt.Errorf("error deleting option %d: %w", id, err)
		}
	}

	return nil
}
