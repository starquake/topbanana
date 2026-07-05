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

func TestGenerateInviteToken_PairMatches(t *testing.T) {
	t.Parallel()

	raw, hash, err := GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if got, want := hash, HashInviteToken(raw); got != want {
		t.Errorf("HashInviteToken(raw) = %q, want %q", got, want)
	}
}

func TestSendInviteEmail_StoresAndSends(t *testing.T) {
	t.Parallel()

	invites := &recordingInviteStore{}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	err := SendInviteEmail(t.Context(), invites, mailerStub,
		"https://topbanana.example", "alice@example.test", "vip", locale.LocaleEN, 7, now)
	if err != nil {
		t.Fatalf("SendInviteEmail err = %v, want nil", err)
	}

	if got, want := len(invites.created), 1; got != want {
		t.Fatalf("invites.created len = %d, want %d", got, want)
	}
	rec := invites.created[0]
	if got, want := rec.email, "alice@example.test"; got != want {
		t.Errorf("CreateInvite email = %q, want %q", got, want)
	}
	if got, want := rec.invitedByPlayerID, int64(7); got != want {
		t.Errorf("CreateInvite invitedByPlayerID = %d, want %d", got, want)
	}
	if got, want := rec.note, "vip"; got != want {
		t.Errorf("CreateInvite note = %q, want %q", got, want)
	}
	if got, want := rec.expiresAt, now.Add(InviteTokenTTL); !got.Equal(want) {
		t.Errorf("CreateInvite expiresAt = %v, want %v (7-day TTL)", got, want)
	}
	if got, want := len(rec.tokenHash), 64; got != want {
		t.Errorf("CreateInvite tokenHash len = %d, want %d", got, want)
	}

	if got, want := len(mailerStub.sent), 1; got != want {
		t.Fatalf("mailer.sent len = %d, want %d", got, want)
	}
	msg := mailerStub.sent[0]
	if got, want := msg.To, "alice@example.test"; got != want {
		t.Errorf("msg.To = %q, want %q", got, want)
	}
	if got, want := msg.Kind, mailer.KindInvite; got != want {
		t.Errorf("msg.Kind = %q, want %q", got, want)
	}
	if got, want := msg.Subject, "You are invited to Top Banana!"; got != want {
		t.Errorf("msg.Subject = %q, want %q", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/accept-invite?token=") {
		t.Errorf("msg.Body missing accept-invite link, got %q", msg.Body)
	}
}

func TestSendInviteEmail_Dutch(t *testing.T) {
	t.Parallel()

	invites := &recordingInviteStore{}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	err := SendInviteEmail(t.Context(), invites, mailerStub,
		"https://topbanana.example", "alice@example.test", "", locale.LocaleNL, 7, now)
	if err != nil {
		t.Fatalf("SendInviteEmail err = %v, want nil", err)
	}

	msg := mailerStub.sent[0]
	if got, want := msg.Subject, "Je bent uitgenodigd voor Top Banana!"; got != want {
		t.Errorf("msg.Subject = %q, want %q", got, want)
	}
	if got, want := msg.Body, "Deze link is 7 dagen geldig"; !strings.Contains(got, want) {
		t.Errorf("msg.Body = %q, should contain %q", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/accept-invite?token=") {
		t.Errorf("msg.Body missing accept-invite link, got %q", msg.Body)
	}
}

func TestSendInviteEmail_StoreFailureSkipsSend(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("disk full")
	invites := &recordingInviteStore{createErr: wantErr}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	err := SendInviteEmail(t.Context(), invites, mailerStub,
		"https://topbanana.example", "alice@example.test", "", locale.LocaleEN, 1, now)
	if got, want := err, wantErr; !errors.Is(got, want) {
		t.Errorf("err = %v, want wrapping %v", got, want)
	}
	if got, want := len(mailerStub.sent), 0; got != want {
		t.Errorf("mailer.sent len = %d, want %d (store failure must not send)", got, want)
	}
}

func TestResendInviteEmail_RotatesAndSends(t *testing.T) {
	t.Parallel()

	invites := &recordingInviteStore{rotateMail: "bob@example.test"}
	mailerStub := &recordingSender{}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	err := ResendInviteEmail(t.Context(), invites, mailerStub, "https://topbanana.example", locale.LocaleEN, 42, now)
	if err != nil {
		t.Fatalf("ResendInviteEmail err = %v, want nil", err)
	}

	if got, want := len(invites.rotated), 1; got != want {
		t.Fatalf("invites.rotated len = %d, want %d", got, want)
	}
	rec := invites.rotated[0]
	if got, want := rec.id, int64(42); got != want {
		t.Errorf("RotateInviteToken id = %d, want %d", got, want)
	}
	if got, want := rec.expiresAt, now.Add(InviteTokenTTL); !got.Equal(want) {
		t.Errorf("RotateInviteToken expiresAt = %v, want %v (7-day TTL)", got, want)
	}
	if got, want := len(rec.tokenHash), 64; got != want {
		t.Errorf("RotateInviteToken tokenHash len = %d, want %d", got, want)
	}

	if got, want := len(mailerStub.sent), 1; got != want {
		t.Fatalf("mailer.sent len = %d, want %d", got, want)
	}
	msg := mailerStub.sent[0]
	if got, want := msg.To, "bob@example.test"; got != want {
		t.Errorf("msg.To = %q, want %q (the rotated recipient)", got, want)
	}
	if !strings.Contains(msg.Body, "https://topbanana.example/accept-invite?token=") {
		t.Errorf("msg.Body missing accept-invite link, got %q", msg.Body)
	}
}

func TestResendInviteEmail_RotateFailureSkipsSend(t *testing.T) {
	t.Parallel()

	invites := &recordingInviteStore{rotateErr: ErrInviteNotPending}
	mailerStub := &recordingSender{}

	err := ResendInviteEmail(
		t.Context(), invites, mailerStub, "https://topbanana.example", locale.LocaleEN, 1, time.Now())
	if got, want := err, ErrInviteNotPending; !errors.Is(got, want) {
		t.Errorf("err = %v, want wrapping %v", got, want)
	}
	if got, want := len(mailerStub.sent), 0; got != want {
		t.Errorf("mailer.sent len = %d, want %d (rotate failure must not send)", got, want)
	}
}

func TestBuildInviteLink_HappyPath(t *testing.T) {
	t.Parallel()

	invites := &recordingInviteStore{}
	mailerStub := &recordingSender{}
	if err := SendInviteEmail(t.Context(), invites, mailerStub,
		"https://topbanana.example", "x@example.test", "", locale.LocaleEN, 1, time.Now()); err != nil {
		t.Fatalf("SendInviteEmail err = %v, want nil", err)
	}
	body := mailerStub.sent[0].Body
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
	if got, want := u.Path, "/accept-invite"; got != want {
		t.Errorf("link.Path = %q, want %q", got, want)
	}
	if got := u.Query().Get("token"); got == "" {
		t.Error("link query missing token")
	}
}

type recordingInviteStore struct {
	mu         sync.Mutex
	created    []createInviteCall
	createErr  error
	rotated    []rotateInviteCall
	rotateErr  error
	rotateMail string
}

type createInviteCall struct {
	email             string
	tokenHash         string
	note              string
	invitedByPlayerID int64
	expiresAt         time.Time
}

type rotateInviteCall struct {
	id        int64
	tokenHash string
	expiresAt time.Time
}

func (s *recordingInviteStore) CreateInvite(
	_ context.Context, email, tokenHash, note string, invitedByPlayerID int64, expiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, createInviteCall{
		email:             email,
		tokenHash:         tokenHash,
		note:              note,
		invitedByPlayerID: invitedByPlayerID,
		expiresAt:         expiresAt,
	})

	return nil
}

func (*recordingInviteStore) GetLiveInvite(_ context.Context, _ string) (*LiveInvite, error) {
	return nil, errors.ErrUnsupported
}

func (*recordingInviteStore) ConsumeInvite(_ context.Context, _ string) error {
	return errors.ErrUnsupported
}

func (*recordingInviteStore) DeleteExpiredInvites(_ context.Context) error {
	return nil
}

func (*recordingInviteStore) ListPendingInvites(_ context.Context) ([]*PendingInvite, error) {
	return nil, errors.ErrUnsupported
}

func (*recordingInviteStore) RevokeInvite(_ context.Context, _ int64) error {
	return errors.ErrUnsupported
}

func (s *recordingInviteStore) RotateInviteToken(
	_ context.Context, id int64, newTokenHash string, newExpiresAt time.Time,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rotateErr != nil {
		return "", s.rotateErr
	}
	s.rotated = append(s.rotated, rotateInviteCall{
		id:        id,
		tokenHash: newTokenHash,
		expiresAt: newExpiresAt,
	})

	return s.rotateMail, nil
}
