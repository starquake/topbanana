package profile_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/mailer"
	. "github.com/starquake/topbanana/internal/profile"
	"github.com/starquake/topbanana/internal/store"
)

func TestValidateEmailChange_Cases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		newEmail     string
		currentEmail string
		wantOK       bool
		wantMsgPart  string
	}{
		{
			name:         "blank rejected",
			newEmail:     "",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "Enter a new email",
		},
		{
			name:         "malformed rejected",
			newEmail:     "not-an-email",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "valid email",
		},
		{
			name:         "matches current rejected",
			newEmail:     "current@example.test",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "already your address",
		},
		{
			name:         "matches current case-insensitive rejected",
			newEmail:     "current@example.test",
			currentEmail: "CURRENT@Example.Test",
			wantOK:       false,
			wantMsgPart:  "already your address",
		},
		{
			name:         "fresh value accepted",
			newEmail:     "fresh@example.test",
			currentEmail: "current@example.test",
			wantOK:       true,
			wantMsgPart:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, ok := ExportValidateEmailChange(tc.newEmail, tc.currentEmail)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("ok = %v, want %v (msg=%q)", got, want, msg)
			}
			if tc.wantMsgPart != "" {
				if got, want := msg, tc.wantMsgPart; !strings.Contains(got, want) {
					t.Errorf("msg = %q, should contain %q", got, want)
				}
			}
		})
	}
}

// emailStubTokens implements auth.VerifyTokenStore and counts the
// tokens minted so a test can assert that a rejected change mints
// nothing.
type emailStubTokens struct {
	mu      sync.Mutex
	created int
}

func (s *emailStubTokens) CreateVerifyToken(_ context.Context, _ string, _ int64, _ time.Time, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created++

	return nil
}

func (*emailStubTokens) ConsumeVerifyToken(_ context.Context, _ string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (*emailStubTokens) DeleteExpiredVerifyTokens(_ context.Context) error {
	return errors.ErrUnsupported
}

// emailStubSender captures every message Send receives and signals on
// a buffered channel so a test can wait for the detached dispatch
// goroutine to finish.
type emailStubSender struct {
	mu   sync.Mutex
	msgs []mailer.Message
	got  chan struct{}
}

func newEmailStubSender() *emailStubSender {
	return &emailStubSender{got: make(chan struct{}, 8)}
}

func (s *emailStubSender) Send(_ context.Context, msg mailer.Message) error {
	s.mu.Lock()
	s.msgs = append(s.msgs, msg)
	s.mu.Unlock()
	s.got <- struct{}{}

	return nil
}

func (s *emailStubSender) snapshot() []mailer.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]mailer.Message, len(s.msgs))
	copy(out, s.msgs)

	return out
}

// waitForSends blocks until n messages have been observed or the test
// deadline fires, so the assertions on the detached goroutine are not
// racy.
func (s *emailStubSender) waitForSends(t *testing.T, n int) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for range n {
		select {
		case <-s.got:
		case <-deadline:
			t.Fatalf("timed out waiting for %d sends, got %d", n, len(s.snapshot()))
		}
	}
}

// emailChangeOldAddr / emailChangeNewAddr are the current and target
// addresses every handler test submits. The new address is never
// seeded, so the real GetPlayerByEmail reports it free.
const (
	emailChangeOldAddr = "old@example.test"
	emailChangeNewAddr = "new@example.test"
)

// emailChangeResult bundles everything postEmailChange surfaces so the
// helper stays under revive's return-result cap.
type emailChangeResult struct {
	sender *emailStubSender
	tokens *emailStubTokens
	logs   string
	rec    *httptest.ResponseRecorder
}

// postEmailChange drives HandleProfileEmailChange with the given
// player and current password against emailChangeNewAddr, backed by
// players: a real PlayerStore that holds the in-context password player
// (when seeded) and no row for the new address, so the real
// GetPlayerByEmail lookup for the target hits the free path.
func postEmailChange(
	t *testing.T,
	players *store.PlayerStore,
	player *auth.Player,
	currentPassword string,
) emailChangeResult {
	t.Helper()

	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	sender := newEmailStubSender()
	tokens := &emailStubTokens{}
	flash := auth.NewSignedFlash(
		[]byte("test-key-32-bytes-test-key-32byt"), false,
		EmailChangeFlashCookieName, EmailChangeFlashCookiePath,
	)
	deps := EmailChangeDeps{
		Players: players,
		Tokens:  tokens,
		Sender:  sender,
		Flash:   flash,
		BaseURL: "https://topbanana.test",
	}
	handler := HandleProfileEmailChange(logger, deps)

	form := url.Values{"new_email": {emailChangeNewAddr}}
	if currentPassword != "" {
		form.Set("current_password", currentPassword)
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/profile/email", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithPlayer(req.Context(), player))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return emailChangeResult{sender: sender, tokens: tokens, logs: logs.String(), rec: rec}
}

// seedPasswordPlayer creates a real PlayerStore, inserts a
// password-bearing player at emailChangeOldAddr, and returns both. The
// persisted row is what the handler reads from context; the new address
// is left unseeded so the real GetPlayerByEmail lookup hits the free path.
func seedPasswordPlayer(t *testing.T) (*store.PlayerStore, *auth.Player) {
	t.Helper()

	hash, err := auth.HashPassword("correct-battery-13")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}

	players := store.NewPlayerStore(dbtest.Open(t), slog.New(slog.DiscardHandler))
	player, err := players.CreatePlayer(t.Context(), "old-player", emailChangeOldAddr, hash, auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	return players, player
}

func TestHandleProfileEmailChange_WrongPasswordRejected(t *testing.T) {
	t.Parallel()

	players, player := seedPasswordPlayer(t)
	res := postEmailChange(t, players, player, "wrong-password")

	if got, want := res.rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.tokens.created, 0; got != want {
		t.Errorf("tokens created = %d, want %d (rejected change must mint nothing)", got, want)
	}
	if got, want := len(res.sender.snapshot()), 0; got != want {
		t.Errorf("sends = %d, want %d (rejected change must not send mail)", got, want)
	}
	if got, want := res.logs, "profile email change rejected: current password incorrect"; !strings.Contains(
		got,
		want,
	) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	if got, want := res.logs, "level=INFO"; !strings.Contains(got, want) {
		t.Errorf("log = %q, rejection should log at INFO", got)
	}
}

func TestHandleProfileEmailChange_EmptyPasswordRejected(t *testing.T) {
	t.Parallel()

	players, player := seedPasswordPlayer(t)
	res := postEmailChange(t, players, player, "")

	if got, want := res.rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.tokens.created, 0; got != want {
		t.Errorf("tokens created = %d, want %d (empty password must mint nothing)", got, want)
	}
	if got, want := len(res.sender.snapshot()), 0; got != want {
		t.Errorf("sends = %d, want %d (empty password must not send mail)", got, want)
	}
	if got, want := res.logs, "profile email change rejected: current password incorrect"; !strings.Contains(
		got,
		want,
	) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
}

func TestHandleProfileEmailChange_CorrectPasswordDispatchesAndNotifiesOldAddress(t *testing.T) {
	t.Parallel()

	players, player := seedPasswordPlayer(t)
	res := postEmailChange(t, players, player, "correct-battery-13")

	if got, want := res.rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	res.sender.waitForSends(t, 2)

	if got, want := res.tokens.created, 1; got != want {
		t.Errorf("tokens created = %d, want %d", got, want)
	}

	var verifyTo, noticeTo string
	for _, m := range res.sender.snapshot() {
		if m.Kind == mailer.KindVerify {
			verifyTo = m.To

			continue
		}
		if m.Kind == mailer.KindEmailChangeNotice {
			noticeTo = m.To
			if got, want := m.Body, emailChangeNewAddr; !strings.Contains(got, want) {
				t.Errorf("notice body = %q, should name the new address %q", got, want)
			}

			continue
		}
		t.Errorf("unexpected mail Kind %q", m.Kind)
	}
	if got, want := verifyTo, emailChangeNewAddr; got != want {
		t.Errorf("verify mail To = %q, want new address %q", got, want)
	}
	if got, want := noticeTo, emailChangeOldAddr; got != want {
		t.Errorf("notice mail To = %q, want old address %q", got, want)
	}
}

func TestHandleProfileEmailChange_OAuthOnlyBlocked(t *testing.T) {
	t.Parallel()

	// OAuth-only accounts are blocked before the store lookup, so an empty
	// store is enough; the password player is intentionally not seeded.
	players := store.NewPlayerStore(dbtest.Open(t), slog.New(slog.DiscardHandler))
	player := &auth.Player{ID: 7, Email: emailChangeOldAddr, PasswordHash: ""}
	res := postEmailChange(t, players, player, "")

	if got, want := res.rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.tokens.created, 0; got != want {
		t.Errorf("tokens created = %d, want %d (OAuth-only account cannot self-change email)", got, want)
	}
	if got, want := len(res.sender.snapshot()), 0; got != want {
		t.Errorf("sends = %d, want %d (blocked change must not send mail)", got, want)
	}
	if got, want := res.logs, "profile email change blocked: account has no password"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
}
