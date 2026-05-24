package auth

import (
	"context"
	"errors"
	"log/slog"
)

// migrateGamesAfterSignIn is the post-auth hook that carries an
// anonymous visitor's pre-sign-in game history onto the account they
// just signed into (#406).
//
// Runs after sessions.Set in HandleLoginSubmit and
// HandleGoogleCallback. The four short-circuits keep the migration
// limited to the case it was designed for:
//
//  1. priorSessionPlayerID is nil — visitor arrived without a session,
//     nothing to migrate from.
//  2. priorSessionPlayerID equals signedInPlayerID — the auth flow
//     claimed the visitor's existing row in place (the password-claim
//     path in HandleRegisterSubmit, the session-claim path in
//     linkOrCreateGooglePlayer); the data is already on the right
//     player, no move needed.
//  3. The prior row no longer exists — race with another callback that
//     already cleaned it up, or a manual admin delete.
//  4. The prior row is NOT anonymous — visitor switched between two
//     credentialled accounts on purpose; moving the previous account's
//     game history would be data corruption, not a migration.
//
// Silent on success: no UI prompt today. The ticket's recommended
// "Yes, move it?" confirm modal is a follow-up that lives on top of
// this backend. Failure here is logged but does NOT fail the sign-in
// — the visitor is already authenticated; refusing to redirect them
// because an unrelated cleanup failed would be worse UX than the
// orphaned-games state itself.
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
