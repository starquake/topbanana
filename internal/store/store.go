// Package store provides the application's data stores.
package store

import (
	"database/sql"
	"log/slog"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// Stores is a collection of stores for the application.
//
// GameStore satisfies both game.Store (the broad interface the game
// service uses) and auth.AnonymousGameMigrator (the narrow one the
// post-sign-in migration uses); both slots point at the same concrete
// instance so consumers only see the methods relevant to their flow.
// The same pattern applies to PlayerStore, which satisfies
// auth.PlayerStore, auth.OAuthIdentityStore, auth.PlayerLister, and
// auth.AdminPlayerStore.
type Stores struct {
	Quizzes      quiz.Store
	Games        game.Store
	GameMigrator auth.AnonymousGameMigrator
	Players      auth.PlayerStore
	OAuth        auth.OAuthIdentityStore
	PlayerLister auth.PlayerLister
	AdminPlayers auth.AdminPlayerStore
	AdminList    auth.AdminListStore
	VerifyTokens auth.VerifyTokenStore
	ResetTokens  auth.ResetTokenStore
	Invites      auth.InviteStore
	// InvitePlayers is the narrow create+verify+read slice the
	// accept-invite flow uses; backed by the same PlayerStore instance.
	InvitePlayers auth.InvitePlayerStore
	Home          home.Store
	Retention     *RetentionStore
	LiveSessions  livesession.Store
}

// New initializes a new Stores instance with the provided database connection.
//
// PlayerStore satisfies auth.PlayerStore, auth.OAuthIdentityStore,
// auth.PlayerLister, and auth.AdminPlayerStore; callers receive the same
// concrete instance through every interface slot so they only see the
// methods relevant to their flow.
func New(conn *sql.DB, logger *slog.Logger) *Stores {
	players := NewPlayerStore(conn, logger)
	games := NewGameStore(conn, logger)

	return &Stores{
		Quizzes:       NewQuizStore(conn, logger),
		Games:         games,
		GameMigrator:  games,
		Players:       players,
		OAuth:         players,
		PlayerLister:  players,
		AdminPlayers:  players,
		AdminList:     players,
		VerifyTokens:  players,
		ResetTokens:   players,
		Invites:       players,
		InvitePlayers: players,
		Home:          NewHomeStore(conn, logger),
		Retention:     NewRetentionStore(conn, logger),
		LiveSessions:  NewLiveSessionStore(conn, logger),
	}
}
