package auth_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

func TestGenerateResetToken_PairMatches(t *testing.T) {
	t.Parallel()

	raw, hash, err := GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if got, want := hash, HashResetToken(raw); got != want {
		t.Errorf("HashResetToken(raw) = %q, want %q", got, want)
	}
}

func TestSendResetEmail_StoresAndSends(t *testing.T) {
	t.Parallel()

	tokens := &recordingResetTokenStore{}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	err := SendResetEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", locale.LocaleEN, 42, now)
	if err != nil {
		t.Fatalf("SendResetEmail err = %v, want nil", err)
	}

	if got, want := len(tokens.created), 1; got != want {
		t.Fatalf("tokens.created len = %d, want %d", got, want)
	}
	rec := tokens.created[0]
	if got, want := rec.playerID, int64(42); got != want {
		t.Errorf("CreateResetToken playerID = %d, want %d", got, want)
	}
	if got, want := rec.expiresAt, now.Add(ResetTokenTTL); !got.Equal(want) {
		t.Errorf("CreateResetToken expiresAt = %v, want %v", got, want)
	}
	if got, want := len(rec.tokenHash), 64; got != want {
		t.Errorf("CreateResetToken tokenHash len = %d, want %d", got, want)
	}

	if got, want := len(mailerStub.sent), 1; got != want {
		t.Fatalf("mailer.sent len = %d, want %d", got, want)
	}
	msg := mailerStub.sent[0]
	if got, want := msg.To, "alice@example.test"; got != want {
		t.Errorf("msg.To = %q, want %q", got, want)
	}
	if got, want := msg.Kind, mailer.KindReset; got != want {
		t.Errorf("msg.Kind = %q, want %q", got, want)
	}
	if got, want := msg.Subject, "Reset your Top Banana! password"; got != want {
		t.Errorf("msg.Subject = %q, want %q", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/reset-password?token=") {
		t.Errorf("msg.Body missing reset link, got %q", msg.Body)
	}
}

func TestSendResetEmail_Dutch(t *testing.T) {
	t.Parallel()

	tokens := &recordingResetTokenStore{}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	err := SendResetEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", locale.LocaleNL, 42, now)
	if err != nil {
		t.Fatalf("SendResetEmail err = %v, want nil", err)
	}

	msg := mailerStub.sent[0]
	if got, want := msg.Subject, "Stel je Top Banana!-wachtwoord opnieuw in"; got != want {
		t.Errorf("msg.Subject = %q, want %q", got, want)
	}
	if got, want := msg.Body, "Deze link is 30 minuten geldig"; !strings.Contains(got, want) {
		t.Errorf("msg.Body = %q, should contain %q", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/reset-password?token=") {
		t.Errorf("msg.Body missing reset link, got %q", msg.Body)
	}
}

func TestSendResetEmail_StoreFailureSkipsSend(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("disk full")
	tokens := &recordingResetTokenStore{createErr: wantErr}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	err := SendResetEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", locale.LocaleEN, 1, now)
	if got, want := err, wantErr; !errors.Is(got, want) {
		t.Errorf("err = %v, want wrapping %v", got, want)
	}
	if got, want := len(mailerStub.sent), 0; got != want {
		t.Errorf("mailer.sent len = %d, want %d (store failure must not send)", got, want)
	}
}

func TestBuildResetLink_HappyPath(t *testing.T) {
	t.Parallel()

	// BuildResetLink is exercised indirectly through SendResetEmail's
	// body assertion, but we want a direct path-shape test too.
	tokens := &recordingResetTokenStore{}
	mailerStub := &recordingSender{}
	if err := SendResetEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "x@example.test", locale.LocaleEN, 1, time.Now()); err != nil {
		t.Fatalf("SendResetEmail err = %v, want nil", err)
	}
	body := mailerStub.sent[0].Body
	// Parse the embedded link to confirm the shape: scheme + host +
	// /reset-password path + token query.
	start := strings.Index(body, "https://")
	if start < 0 {
		t.Fatalf("body has no https:// link, got %q", body)
	}
	end := strings.Index(body[start:], "\n")
	if end < 0 {
		t.Fatalf("body link not terminated by newline, got %q", body)
	}
	link := body[start : start+end]
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("url.Parse(%q) err = %v, want nil", link, err)
	}
	if got, want := u.Path, "/reset-password"; got != want {
		t.Errorf("link.Path = %q, want %q", got, want)
	}
	if got := u.Query().Get("token"); got == "" {
		t.Error("link query missing token")
	}
}

type recordingResetTokenStore struct {
	mu        sync.Mutex
	created   []createResetTokenCall
	createErr error
}

type createResetTokenCall struct {
	tokenHash string
	playerID  int64
	expiresAt time.Time
}

func (s *recordingResetTokenStore) CreateResetToken(
	_ context.Context, tokenHash string, playerID int64, expiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, createResetTokenCall{
		tokenHash: tokenHash,
		playerID:  playerID,
		expiresAt: expiresAt,
	})

	return nil
}

func (s *recordingResetTokenStore) Created() []createResetTokenCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]createResetTokenCall, len(s.created))
	copy(out, s.created)

	return out
}

func (*recordingResetTokenStore) ConsumeResetToken(_ context.Context, _, _ string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (*recordingResetTokenStore) LookupResetToken(_ context.Context, _ string) (int64, bool, error) {
	return 0, false, errors.ErrUnsupported
}

func (*recordingResetTokenStore) DeleteExpiredResetTokens(_ context.Context) error {
	return nil
}
