// Package store provides the application's data stores.
package store

import (
	"database/sql"
	"log/slog"

	"github.com/starquake/topbanana/internal/quiz"
)

// Stores is a collection of stores for the application.
type Stores struct {
	Quizzes quiz.Store
}

// New initializes a new Stores instance with the provided database connection.
func New(conn *sql.DB, logger *slog.Logger) *Stores {
	return &Stores{
		Quizzes: NewQuizStore(conn, logger),
	}
}
