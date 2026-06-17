package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/mailer"
)

// dispatchAdminResendVerification mints + dispatches the verification
// email on a detached goroutine. Mirrors auth.dispatchVerifyEmail so a
// closed browser tab does not cancel the send and SMTP latency is not
// observable from the redirect timing. Returns false without dispatching
// when email is not configured (nil tokens/sender or empty baseURL) so
// the caller can skip the audit row and the success notice.
func dispatchAdminResendVerification(
	ctx context.Context,
	logger *slog.Logger,
	tokens auth.VerifyTokenStore,
	sender auth.VerifyEmailSender,
	baseURL, recipient string,
	playerID int64,
	tasks *bgtasks.Tracker,
) bool {
	if tokens == nil || sender == nil {
		return false
	}
	if baseURL == "" {
		logger.WarnContext(ctx, "admin resend verification skipped: BASE_URL is empty",
			slog.Int64("player_id", playerID))

		return false
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+15*time.Second)
	tasks.Go(func() {
		defer cancel()
		auth.SendVerifyEmailBestEffort(bg, logger, tokens, sender,
			baseURL, recipient, playerID, time.Now().UTC())
	})

	return true
}
