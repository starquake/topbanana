package auth_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/mailer"
)

func TestGenerateVerifyToken_PairMatches(t *testing.T) {
	t.Parallel()

	raw, hash, err := GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if got, want := hash, HashVerifyToken(raw); got != want {
		t.Errorf("HashVerifyToken(raw) = %q, want %q", got, want)
	}
}

func TestGenerateVerifyToken_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 32)
	for range 32 {
		raw, _, err := GenerateVerifyToken()
		if err != nil {
			t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
		}
		if _, dup := seen[raw]; dup {
			t.Fatalf("GenerateVerifyToken returned a duplicate token after %d draws", len(seen))
		}
		seen[raw] = struct{}{}
	}
}

func TestHashVerifyToken_Deterministic(t *testing.T) {
	t.Parallel()

	const sample = "deadbeef"
	if got, want := HashVerifyToken(sample), HashVerifyToken(sample); got != want {
		t.Errorf("HashVerifyToken(%q) = %q, want %q", sample, got, want)
	}
	if got, want := HashVerifyToken("a"), HashVerifyToken("b"); got == want {
		t.Errorf("HashVerifyToken(%q) collides with HashVerifyToken(%q)", "a", "b")
	}
}

func TestBuildVerifyLink_HappyPath(t *testing.T) {
	t.Parallel()

	link, err := BuildVerifyLink("https://topbanana.example", "abc123")
	if err != nil {
		t.Fatalf("BuildVerifyLink err = %v, want nil", err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("url.Parse(%q) err = %v, want nil", link, err)
	}
	if got, want := u.Path, "/verify-email"; got != want {
		t.Errorf("link.Path = %q, want %q", got, want)
	}
	if got, want := u.Query().Get("token"), "abc123"; got != want {
		t.Errorf("link.Query()[token] = %q, want %q", got, want)
	}
}

func TestBuildVerifyLink_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	link, err := BuildVerifyLink("https://topbanana.example/", "tok")
	if err != nil {
		t.Fatalf("BuildVerifyLink err = %v, want nil", err)
	}
	if got, want := link, "https://topbanana.example/verify-email?token=tok"; got != want {
		t.Errorf("BuildVerifyLink trailing slash = %q, want %q", got, want)
	}
}

func TestBuildVerifyLink_BaseURLEmpty(t *testing.T) {
	t.Parallel()

	_, err := BuildVerifyLink("", "tok")
	if got, want := err, ErrVerifyBaseURLEmpty; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestBuildVerifyLink_BaseURLMissingScheme(t *testing.T) {
	t.Parallel()

	_, err := BuildVerifyLink("topbanana.example", "tok")
	if got, want := err, ErrVerifyBaseURLInvalid; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestSendVerifyEmail_StoresHashSendsMessage(t *testing.T) {
	t.Parallel()

	tokens := &recordingVerifyTokenStore{}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	err := SendVerifyEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", 42, now)
	if err != nil {
		t.Fatalf("SendVerifyEmail err = %v, want nil", err)
	}

	if got, want := len(tokens.created), 1; got != want {
		t.Fatalf("tokens.created len = %d, want %d", got, want)
	}
	rec := tokens.created[0]
	if got, want := rec.playerID, int64(42); got != want {
		t.Errorf("CreateVerifyToken playerID = %d, want %d", got, want)
	}
	if got, want := rec.expiresAt, now.Add(VerifyTokenTTL); !got.Equal(want) {
		t.Errorf("CreateVerifyToken expiresAt = %v, want %v", got, want)
	}
	if got, want := len(rec.tokenHash), 64; got != want {
		t.Errorf("CreateVerifyToken tokenHash len = %d, want %d", got, want)
	}

	if got, want := len(mailerStub.sent), 1; got != want {
		t.Fatalf("mailer.sent len = %d, want %d", got, want)
	}
	msg := mailerStub.sent[0]
	if got, want := msg.To, "alice@example.test"; got != want {
		t.Errorf("msg.To = %q, want %q", got, want)
	}
	if got, want := msg.Kind, mailer.KindVerify; got != want {
		t.Errorf("msg.Kind = %q, want %q", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/verify-email?token=") {
		t.Errorf("msg.Body missing verify link, got %q", msg.Body)
	}
}

func TestSendVerifyEmail_StoreFailureSkipsSend(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("disk full")
	tokens := &recordingVerifyTokenStore{createErr: wantErr}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	err := SendVerifyEmail(t.Context(), tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", 1, now)
	if got, want := err, wantErr; !errors.Is(got, want) {
		t.Errorf("err = %v, want wrapping %v", got, want)
	}
	if got, want := len(mailerStub.sent), 0; got != want {
		t.Errorf("mailer.sent len = %d, want %d (store failure must not send)", got, want)
	}
}

func TestSendVerifyEmailBestEffort_SwallowsError(t *testing.T) {
	t.Parallel()

	tokens := &recordingVerifyTokenStore{createErr: errors.New("disk full")}
	mailerStub := &recordingSender{}
	logger := slog.New(slog.DiscardHandler)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	// Best-effort path must not panic or block when the store fails.
	SendVerifyEmailBestEffort(t.Context(), logger, tokens, mailerStub,
		"https://topbanana.example", "alice@example.test", 1, now)

	if got, want := len(mailerStub.sent), 0; got != want {
		t.Errorf("mailer.sent len = %d, want %d", got, want)
	}
}

type recordingVerifyTokenStore struct {
	created   []createVerifyTokenCall
	createErr error
}

type createVerifyTokenCall struct {
	tokenHash string
	playerID  int64
	expiresAt time.Time
}

func (s *recordingVerifyTokenStore) CreateVerifyToken(
	_ context.Context, tokenHash string, playerID int64, expiresAt time.Time,
) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, createVerifyTokenCall{
		tokenHash: tokenHash,
		playerID:  playerID,
		expiresAt: expiresAt,
	})

	return nil
}

func (*recordingVerifyTokenStore) ConsumeVerifyToken(_ context.Context, _ string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (*recordingVerifyTokenStore) DeleteExpiredVerifyTokens(_ context.Context) error {
	return nil
}

type recordingSender struct {
	sent    []mailer.Message
	sendErr error
}

func (s *recordingSender) Send(_ context.Context, msg mailer.Message) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, msg)

	return nil
}
