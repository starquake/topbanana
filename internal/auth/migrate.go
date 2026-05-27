package auth

import (
	"context"
	"errors"
	"log/slog"
)

// migrateGamesAfterSignIn moves an anonymous visitor's game history
// onto the account they just signed into (#406). Refuses the move
// when the prior row is credentialled - that would be data corruption,
// not a migration. Failures are logged but do not fail the sign-in.
func migrateGamesAfterSignIn(
	ctx context.Context,
	logger *slog.Logger,
	players PlayerStore,
	games AnonymousGameMigrator,
	priorSessionPlayerID *int64,
	signedInPlayerID int64,
) {
	if games == nil || priorSessionPlayerID == nil {
		return
	}
	if *priorSessionPlayerID == signedInPlayerID {
		return
	}

	prior, err := players.GetPlayerByID(ctx, *priorSessionPlayerID)
	if err != nil {
		if !errors.Is(err, ErrPlayerNotFound) {
			logger.ErrorContext(ctx, "post-signin migrate: lookup prior session player",
				slog.Int64("prior_player_id", *priorSessionPlayerID),
				slog.Any("err", err),
			)
		}

		return
	}
	if !prior.IsAnonymous() {
		return
	}

	moved, err := games.ReattributeGames(ctx, *priorSessionPlayerID, signedInPlayerID)
	if err != nil {
		logger.ErrorContext(ctx, "post-signin migrate: reattribute games",
			slog.Int64("from", *priorSessionPlayerID),
			slog.Int64("to", signedInPlayerID),
			slog.Any("err", err),
		)

		return
	}
	if moved > 0 {
		logger.InfoContext(ctx, "post-signin migrate: reattributed anonymous games",
			slog.Int64("from", *priorSessionPlayerID),
			slog.Int64("to", signedInPlayerID),
			slog.Int64("participants_moved", moved),
		)
	}
}
