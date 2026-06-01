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
	"github.com/starquake/topbanana/internal/mailer"
	. "github.com/starquake/topbanana/internal/profile"
)

// emailStubStore implements auth.PlayerStore for the email-change
// handler tests. GetPlayerByEmail reports the new address as free (the
// dispatch path); every other method returns a sentinel so an
// accidental call is loud.
type emailStubStore struct{}

func (*emailStubStore) GetPlayerByEmail(_ context.Context, _ string) (*auth.Player, error) {
	return nil, auth.ErrPlayerNotFound
}

func (*emailStubStore) GetPlayerByDisplayName(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) GetPlayerByID(_ context.Context, _ int64) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) CreatePlayer(_ context.Context, _, _, _, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) CreateAnonymousPlayer(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) ClaimPlayer(_ context.Context, _ int64, _, _, _, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) RenamePlayer(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*emailStubStore) SetPlayerPasswordHash(_ context.Context, _, _ string) error {
	return errors.ErrUnsupported
}

func (*emailStubStore) ChangePlayerPassword(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*emailStubStore) UpdatePlayerDisplayName(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
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

// emailChangeNewAddr is the new address every handler test submits;
// the dispatch path treats it as free via emailStubStore.
const emailChangeNewAddr = "new@example.test"

// emailChangeResult bundles everything postEmailChange surfaces so the
// helper stays under revive's return-result cap.
type emailChangeResult struct {
	sender *emailStubSender
	tokens *emailStubTokens
	logs   string
	rec    *httptest.ResponseRecorder
}

// postEmailChange drives HandleProfileEmailChange with the given
// player and current password against emailChangeNewAddr.
func postEmailChange(t *testing.T, player *auth.Player, currentPassword string) emailChangeResult {
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
		Players: &emailStubStore{},
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

func passwordPlayer(t *testing.T) *auth.Player {
	t.Helper()

	hash, err := auth.HashPassword("correct-battery-13")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}

	return &auth.Player{ID: 7, Email: "old@example.test", PasswordHash: hash}
}

func TestHandleProfileEmailChange_WrongPasswordRejected(t *testing.T) {
	t.Parallel()

	res := postEmailChange(t, passwordPlayer(t), "wrong-password")

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

	res := postEmailChange(t, passwordPlayer(t), "")

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

	res := postEmailChange(t, passwordPlayer(t), "correct-battery-13")

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
	if got, want := noticeTo, "old@example.test"; got != want {
		t.Errorf("notice mail To = %q, want old address %q", got, want)
	}
}

func TestHandleProfileEmailChange_OAuthOnlyBlocked(t *testing.T) {
	t.Parallel()

	player := &auth.Player{ID: 7, Email: "old@example.test", PasswordHash: ""}
	res := postEmailChange(t, player, "")

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
