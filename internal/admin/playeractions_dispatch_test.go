package admin_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/mailer"
)

// stubVerifyTokenStore satisfies auth.VerifyTokenStore for the dispatch
// test. The methods are no-ops so the spawned send goroutine completes
// without blocking.
type stubVerifyTokenStore struct{}

func (stubVerifyTokenStore) CreateVerifyToken(
	_ context.Context, _ string, _ int64, _ time.Time, _ string,
) error {
	return nil
}

func (stubVerifyTokenStore) ConsumeVerifyToken(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (stubVerifyTokenStore) DeleteExpiredVerifyTokens(_ context.Context) error { return nil }

// stubVerifyEmailSender satisfies auth.VerifyEmailSender; Send is a
// no-op so the spawned goroutine completes promptly.
type stubVerifyEmailSender struct{}

func (stubVerifyEmailSender) Send(_ context.Context, _ mailer.Message) error { return nil }

// TestDispatchAdminResendVerification_BoolContract pins the dispatch
// helper's report of whether it actually sent: false (so the caller
// skips the audit row + success notice) when email is unconfigured,
// true when it spawns the send.
func TestDispatchAdminResendVerification_BoolContract(t *testing.T) {
	t.Parallel()

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
		{name: "nil sender", tokens: stubVerifyTokenStore{}, sender: nil, baseURL: "https://x.test", wantResult: false},
		{
			name:       "empty baseURL",
			tokens:     stubVerifyTokenStore{},
			sender:     stubVerifyEmailSender{},
			baseURL:    "",
			wantResult: false,
		},
		{
			name:       "fully configured",
			tokens:     stubVerifyTokenStore{},
			sender:     stubVerifyEmailSender{},
			baseURL:    "https://x.test",
			wantResult: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := DispatchAdminResendVerification(
				t.Context(), slog.Default(), tc.tokens, tc.sender, tc.baseURL, "to@example.test", 1,
			)
			if want := tc.wantResult; got != want {
				t.Errorf("DispatchAdminResendVerification = %v, want %v", got, want)
			}
		})
	}
}
