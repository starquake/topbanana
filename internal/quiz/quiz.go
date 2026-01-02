// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrQuizNotFound is returned when a quiz is not found.
	ErrQuizNotFound = errors.New("quiz not found")
	// ErrQuestionNotFound is returned when a question is not found.
	ErrQuestionNotFound = errors.New("question not found")
	// ErrUpdatingQuizNoRowsAffected is returned when no rows are affected when updating a quiz.
	ErrUpdatingQuizNoRowsAffected = errors.New("no rows affected when updating quiz")
	// ErrUpdatingQuestionNoRowsAffected is returned when no rows are affected when updating a question.
	ErrUpdatingQuestionNoRowsAffected = errors.New("no rows affected when updating question")
	// ErrDeletingQuestionNoRowsAffected is returned when no rows are affected when deleting a quiz.
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
	// ListQuizzes returns all quizzes.
	ListQuizzes(ctx context.Context) ([]*Quiz, error)
	// GetQuiz returns a quiz including related questions and options by its ID.
	// Returns ErrQuizNotFound if the quiz is not found.
	GetQuiz(ctx context.Context, id int64) (*Quiz, error)
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, qz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, qz *Quiz) error
	// GetQuestion returns a question with options, by its question ID.
	GetQuestion(ctx context.Context, id int64) (*Question, error)
	// CreateQuestion creates a question.
	CreateQuestion(ctx context.Context, qs *Question) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, qs *Question) error
}
