package admin

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/locale"
)

// HandlePlayerResendVerification handles
// POST /admin/players/{playerID}/resend-verification. Per-target
// rate-limited (one resend per minute per playerID) so a stuck operator
// hitting the button does not turn the admin page into a mail floodgate.
// The send itself runs on a detached goroutine so the response is not
// held open while SMTP dials.
func HandlePlayerResendVerification(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	tokens auth.VerifyTokenStore,
	sender auth.VerifyEmailSender,
	baseURL string,
	limiter *PerTargetLimiter,
	flash *auth.SignedFlash,
	tasks *bgtasks.Tracker,
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
		if detail.OnboardingState != auth.OnboardingStateUnverified || detail.Email == "" {
			flash.SetError(w, "Resend is only available for unverified players with an email on file.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		wait, allowed, token := limiter.Allow(playerID)
		if !allowed {
			seconds := max(int((wait+time.Second-1)/time.Second), 1)
			flash.SetError(w, "Slow down: wait a moment before resending.", seconds)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if !dispatchAdminResendVerification(
			r.Context(), logger, tokens, sender, baseURL, detail.Email, locale.Resolve(r), playerID, tasks,
		) {
			// No mail went out (email not configured), so roll the stamp
			// back: an operator on a misconfigured instance is not throttled
			// for a send that never happened (#996). Token-matched so a
			// second concurrent caller's newer stamp is not clobbered.
			limiter.Cancel(playerID, token)
			flash.SetError(w, "Email sending is not configured; no verification email was sent.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionResendVerification, nil)
		flash.SetNotice(w, "Verification email dispatched.")
		redirectToPlayerDetail(w, r, playerID)
	})
}
