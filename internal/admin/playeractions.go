package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
)

// HandlePlayerMarkVerified handles POST /admin/players/{playerID}/verify.
// Only flips when the row is currently unverified; any other state is a
// 400 so a stale browser tab does not silently re-stamp the
// already-verified timestamp.
func HandlePlayerMarkVerified(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		playerID, ok := handlers.ParseIDFromPath(w, r, logger, "playerID")
		if !ok {
			return
		}
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}

		detail, ok := loadActionTarget(w, r, logger, store, playerID)
		if !ok {
			return
		}
		if detail.OnboardingState != auth.OnboardingStateUnverified {
			flash.SetError(w, "This player is not in the 'unverified' state.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if err := store.SetPlayerEmailVerifiedNow(r.Context(), playerID); err != nil {
			logger.ErrorContext(r.Context(), "error marking player verified", slog.Any("err", err))
			flash.SetError(w, "Could not mark player verified. Try again.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionVerify, nil)
		flash.SetNotice(w, "Player marked verified.")
		redirectToPlayerDetail(w, r, playerID)
	})
}

// requireAdminActor returns the signed-in admin from the request
// context, or surfaces a 500 + false when the context is missing the
// player. The auth.RequireAdmin middleware guarantees the player is
// present in production; this guard is a defence-in-depth fallback so
// a misconfigured wiring layer cannot quietly write a NULL actor_id
// into admin_audit.
func requireAdminActor(w http.ResponseWriter, r *http.Request) (*auth.Player, bool) {
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		http.Error(w, "missing actor", http.StatusInternalServerError)

		return nil, false
	}

	return p, true
}

// parseActionForm parses the POST body of a player Set* action, returning
// false (after answering 400) when MaxFormSizeMiddleware's MaxBytesReader
// has tripped. Without this, the lazy PostFormValue parse would swallow
// the error and hand the handler empty values, surfacing a misleading
// validation flash instead of the real "body too large" failure. action
// names the handler in the log line.
func parseActionForm(w http.ResponseWriter, r *http.Request, logger *slog.Logger, action string) bool {
	if err := r.ParseForm(); err != nil {
		logger.InfoContext(r.Context(), action+" form parse failed", slog.Any("err", err))
		http.Error(w, "Form was malformed or too large.", http.StatusBadRequest)

		return false
	}

	return true
}

// loadActionTarget fetches the target player's detail row. A missing
// target yields a 404 + false; any other store error is a 500.
func loadActionTarget(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	store auth.AdminPlayerStore, playerID int64,
) (*auth.PlayerDetail, bool) {
	detail, err := store.GetPlayerDetail(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			http.NotFound(w, r)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error loading action target", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	return detail, true
}
