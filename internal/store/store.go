// Package store provides the application's data stores.
package store

import (
	"database/sql"
	"log/slog"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/quiz"
)

// Stores is a collection of stores for the application.
type Stores struct {
	Quizzes quiz.Store
	Games   game.Store
	Players auth.PlayerStore
	OAuth   auth.OAuthIdentityStore
	Home    home.Store
}

// New initializes a new Stores instance with the provided database connection.
//
// PlayerStore satisfies both auth.PlayerStore and auth.OAuthIdentityStore;
// callers receive the same concrete instance through two interface slots
// so they only see the methods relevant to their flow.
func New(conn *sql.DB, logger *slog.Logger) *Stores {
	players := NewPlayerStore(conn, logger)

	return &Stores{
		Quizzes: NewQuizStore(conn, logger),
		Games:   NewGameStore(conn, logger),
		Players: players,
		OAuth:   players,
		Home:    NewHomeStore(conn, logger),
	}
}
