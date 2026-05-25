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
//
// GameStore satisfies both game.Store (the broad interface the game
// service uses) and auth.AnonymousGameMigrator (the narrow one the
// post-sign-in migration uses); both slots point at the same concrete
// instance so consumers only see the methods relevant to their flow.
// The same pattern applies to PlayerStore, which satisfies
// auth.PlayerStore, auth.OAuthIdentityStore, and auth.PlayerLister.
type Stores struct {
	Quizzes      quiz.Store
	Games        game.Store
	GameMigrator auth.AnonymousGameMigrator
	Players      auth.PlayerStore
	OAuth        auth.OAuthIdentityStore
	PlayerLister auth.PlayerLister
	Home         home.Store
}

// New initializes a new Stores instance with the provided database connection.
//
// PlayerStore satisfies auth.PlayerStore, auth.OAuthIdentityStore, and
// auth.PlayerLister; callers receive the same concrete instance through
// three interface slots so they only see the methods relevant to their flow.
func New(conn *sql.DB, logger *slog.Logger) *Stores {
	players := NewPlayerStore(conn, logger)
	games := NewGameStore(conn, logger)

	return &Stores{
		Quizzes:      NewQuizStore(conn, logger),
		Games:        games,
		GameMigrator: games,
		Players:      players,
		OAuth:        players,
		PlayerLister: players,
		Home:         NewHomeStore(conn, logger),
	}
}
