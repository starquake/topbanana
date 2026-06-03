//go:build integration

package admin_test

import (
	"context"
	"log/slog"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/mailer"
)

// stubVerifyEmailSender satisfies auth.VerifyEmailSender; Send is a
// no-op so the spawned goroutine completes promptly without standing up
// SMTP. It is a legitimate dispatch spy, not a tautological store stub,
// so it stays even though the token store is now real.
type stubVerifyEmailSender struct{}

func (stubVerifyEmailSender) Send(_ context.Context, _ mailer.Message) error { return nil }

// TestDispatchAdminResendVerification_BoolContract pins the dispatch
// helper's report of whether it actually sent: false (so the caller
// skips the audit row + success notice) when email is unconfigured,
// true when it spawns the send. The token store is the real store so
// the "fully configured" branch exercises a genuine CreateVerifyToken
// for the seeded admin (player id 1).
func TestDispatchAdminResendVerification_BoolContract(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)

	tests := []struct {
		name       string
		tokens     auth.VerifyTokenStore
		sender     auth.VerifyEmailSender
		baseURL    string
		wantResult bool
	}{
		{
			name:       "nil tokens",
			tokens:     nil,
			sender:     stubVerifyEmailSender{},
			baseURL:    "https://x.test",
			wantResult: false,
		},
		{name: "nil sender", tokens: env.tokens, sender: nil, baseURL: "https://x.test", wantResult: false},
		{
			name:       "empty baseURL",
			tokens:     env.tokens,
			sender:     stubVerifyEmailSender{},
			baseURL:    "",
			wantResult: false,
		},
		{
			name:       "fully configured",
			tokens:     env.tokens,
			sender:     stubVerifyEmailSender{},
			baseURL:    "https://x.test",
			wantResult: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := DispatchAdminResendVerification(
				t.Context(), slog.Default(), tc.tokens, tc.sender, tc.baseURL, "to@example.test", testAdminID,
			)
			if want := tc.wantResult; got != want {
				t.Errorf("DispatchAdminResendVerification = %v, want %v", got, want)
			}
		})
	}
}
