// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Store represents a store for quizzes.
// This can be implemented for different databases.
type Store interface {
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	// ListQuizzes returns all quizzes.
	ListQuizzes(ctx context.Context) ([]*Quiz, error)
	// QuestionCountsByQuiz returns the number of questions per quiz, keyed by
	// quiz ID. Quizzes with no questions are absent from the map; callers
	// should treat a missing entry as 0. Used alongside ListQuizzes by the
	// admin list to render counts without loading every quiz's full tree.
	QuestionCountsByQuiz(ctx context.Context) (map[int64]int, error)
	// GetQuiz returns a quiz including related questions and options by its ID.
	// Returns ErrQuizNotFound if the quiz is not found.
	GetQuiz(ctx context.Context, id int64) (*Quiz, error)
	// QuizExists is a cheap existence check for a quiz by ID. It runs a
	// single one-row SELECT EXISTS probe and does not load the quiz's
	// questions or options. Prefer this over GetQuiz when the caller only
	// needs to know whether the quiz is real (e.g. to map a missing quiz
	// to a 404) and does not need the rest of the tree.
	QuizExists(ctx context.Context, id int64) (bool, error)
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, qz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, qz *Quiz) error
	// ListQuestions returns all questions for a quiz by its ID.
	ListQuestions(ctx context.Context, quizID int64) ([]*Question, error)
	// GetQuestion returns a question with options, by its question ID.
	GetQuestion(ctx context.Context, questionID int64) (*Question, error)
	// CreateQuestion creates a question.
	CreateQuestion(ctx context.Context, qs *Question) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, qs *Question) error
	// GetOption returns an option by its ID.
	GetOption(ctx context.Context, optionID int64) (*Option, error)
	// GetOptionsByIDs returns options for the given IDs.
	GetOptionsByIDs(ctx context.Context, ids []int64) ([]*Option, error)
	// DeleteQuiz deletes a quiz and all its questions and options by ID.
	DeleteQuiz(ctx context.Context, id int64) error
	// DeleteQuestion deletes a question and all its options by ID.
	DeleteQuestion(ctx context.Context, id int64) error
}

var (
	// ErrQuizNotFound is returned when a quiz is not found.
	ErrQuizNotFound = errors.New("quiz not found")
	// ErrQuestionNotFound is returned when a question is not found.
	ErrQuestionNotFound = errors.New("question not found")
	// ErrOptionNotFound is returned when an option is not found.
	ErrOptionNotFound = errors.New("option not found")
	// ErrUpdatingQuizNoRowsAffected is returned when no rows are affected when updating a quiz.
	ErrUpdatingQuizNoRowsAffected = errors.New("no rows affected when updating quiz")
	// ErrUpdatingQuestionNoRowsAffected is returned when no rows are affected when updating a question.
	ErrUpdatingQuestionNoRowsAffected = errors.New("no rows affected when updating question")
	// ErrDeletingQuizNoRowsAffected is returned when no rows are affected when deleting a quiz.
	ErrDeletingQuizNoRowsAffected = errors.New("no rows affected when deleting quiz")
	// ErrDeletingQuestionNoRowsAffected is returned when no rows are affected when deleting a question.
	ErrDeletingQuestionNoRowsAffected = errors.New("no rows affected when deleting question")
	// ErrUpdatingOptionNoRowsAffected is returned when no rows are affected when updating a option.
	ErrUpdatingOptionNoRowsAffected = errors.New("no rows affected when updating option")
	// ErrDeletingOptionNoRowsAffected is returned when no rows are affected when deleting a option.
	ErrDeletingOptionNoRowsAffected = errors.New("no rows affected when deleting option")
	// ErrCannotUpdateQuizWithIDZero is returned when trying to update a quiz with ID 0.
	ErrCannotUpdateQuizWithIDZero = errors.New("cannot update quiz with ID 0")
	// ErrCannotUpdateQuestionWithIDZero is returned when trying to update a question with ID 0.
	ErrCannotUpdateQuestionWithIDZero = errors.New("cannot update question with ID 0")
)

// Quiz represents a quiz.
type Quiz struct {
	ID          int64
	Title       string
	Slug        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Questions   []*Question
}

// Valid checks if the quiz, its questions, and its options are valid.
func (q *Quiz) Valid(ctx context.Context) map[string]string {
	problems := make(map[string]string)
	if q.Title == "" {
		problems["Title"] = "Title is required"
	}
	if q.Slug == "" {
		problems["Slug"] = "Slug is required"
	}
	if q.Description == "" {
		problems["Description"] = "Description is required"
	}
	for qsIndex, question := range q.Questions {
		if qsProblems := question.Valid(ctx); len(qsProblems) > 0 {
			for qsProblemKey, v := range qsProblems {
				problems[fmt.Sprintf("Questions[%d][%s]", qsIndex, qsProblemKey)] = v
			}
		}
		validQuestionOptions(ctx, question, problems, qsIndex)
	}

	return problems
}

func validQuestionOptions(ctx context.Context, question *Question, problems map[string]string, qsIndex int) {
	// Multi-correct, no-correct, and all-correct configurations are all
	// supported by the data model and the admin UI; this validator only
	// checks per-option text and other field-level constraints.
	for oIndex, option := range question.Options {
		if oProblems := option.Valid(ctx); len(oProblems) > 0 {
			for oProblemKey, v := range oProblems {
				problems[fmt.Sprintf("Questions[%d].Options[%d][%s]", qsIndex, oIndex, oProblemKey)] = v
			}
		}
	}
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
		problems["Text"] = "Text is required"
	}
	if len(q.Options) == 0 {
		problems["Options"] = "Options are required"
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
		problems["Text"] = "Text is required"
	}

	return problems
}
