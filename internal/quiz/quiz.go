// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"database/sql"
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
	Questions   []Question
}

// Question represents a question in a quiz.
type Question struct {
	ID       int64
	QuizID   int
	Text     string
	ImageURL string
	Options  []Option
}

// Option represents an option for a question.
type Option struct {
	ID         int64
	QuestionID int
	Text       string
	Correct    bool
}

// Store represents a store for quizzes.
// This can be implemented for different databases.
type Store interface {
	// Create(ctx context.Context, quiz *Quiz) error
	// GetByID(ctx context.Context, id int64) (*Quiz, error)
	// List returns all quizzes.
	List(ctx context.Context) ([]Quiz, error)
	// Delete(ctx context.Context, id int64) error
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

// func (s *SQLiteStore) Create(ctx context.Context, quiz *Quiz) error {
//	query := `INSERT INTO quizzes (name, slug, description, created_at) VALUES (?, ?, ?, ?)`
//	_, err := s.db.ExecContext(ctx, query, quiz.Title, quiz.Slug, quiz.Description, quiz.CreatedAt)
//	if err != nil {
//		return err
//	}
//	return nil
//}

// List returns all quizzes.
func (s *SQLiteStore) List(ctx context.Context) ([]Quiz, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, title, slug, description, created_at FROM quizzes`,
	)
	if err != nil {
		return nil, fmt.Errorf("error querying quizzes: %w", err)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("error iterating rows: %w", rows.Err())
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			s.logger.Error(ctx, "error closing rows", err)
		}
	}()
	var quizzes []Quiz
	for rows.Next() {
		var quiz Quiz
		err := rows.Scan(&quiz.ID, &quiz.Title, &quiz.Slug, &quiz.Description, &quiz.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}
		quizzes = append(quizzes, quiz)
	}

	return quizzes, nil
}
