package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/mailer"
)

// InviteTokenTTL is the lifetime of an admin-issued invite link (#318).
// 7 days: an invite is a deliberate, human-initiated action whose
// recipient may not check mail for days, so it is far longer-lived than
// a reset (30 min) or verify (24 h) link. The token is still single-use
// and the email is the only place the raw value ever appears.
const InviteTokenTTL = 7 * 24 * time.Hour

// ErrInviteInvalid is returned by ConsumeInvite and the live lookup when
// the supplied token does not match an acceptable row (never existed,
// expired, already accepted, or revoked). Invites are single-use by
// design, so there is no "already accepted" distinction surfaced to the
// caller - the safer UX is a single "this invite link is no longer
// valid" page.
var ErrInviteInvalid = errors.New("invite invalid")

// LiveInvite is the slice of a pending invite row the accept flow needs:
// the row id (for logging), the address the invite was issued to (which
// becomes the new player's email, already verified), and the audit
// actor who sent it.
type LiveInvite struct {
	ID    int64
	Email string
	// InvitedByPlayerID is 0 when the inviting admin's row has since been
	// deleted (the FK is ON DELETE SET NULL, so the invite outlives its
	// actor).
	InvitedByPlayerID int64
}

// InviteStore persists admin-issued invites. Implemented by
// store.PlayerStore against the real DB; the interface lives here so the
// auth package can drive the invite flow without a direct import of the
// storage layer.
type InviteStore interface {
	// CreateInvite records the sha256 hex of a freshly minted invite
	// token. The raw token is never stored - a DB leak should not be
	// replayable. invitedByPlayerID is the admin who sent the invite (0
	// stores NULL); note is an optional free-text reminder ("" stores
	// NULL).
	CreateInvite(
		ctx context.Context, email, tokenHash, note string, invitedByPlayerID int64, expiresAt time.Time,
	) error
	// GetLiveInvite peeks at the row without consuming it. Returns the
	// invite when it exists, is pending, and is unexpired; returns
	// ErrInviteInvalid otherwise. Used by the GET /accept-invite handler
	// to short-circuit the form render for dead links so the recipient is
	// not asked to pick a password the POST will reject. Never a security
	// boundary: the atomic consume on POST is what enforces single-use;
	// this peek only gates the render path.
	GetLiveInvite(ctx context.Context, tokenHash string) (*LiveInvite, error)
	// ConsumeInvite atomically marks the invite accepted and stamps
	// accepted_at. Returns ErrInviteInvalid when no live row matches
	// (never existed, expired, already accepted, or revoked). Single-use:
	// a second consume against the same hash returns ErrInviteInvalid.
	ConsumeInvite(ctx context.Context, tokenHash string) error
	// DeleteExpiredInvites removes still-pending rows whose expires_at has
	// passed. Called from the startup + periodic sweep so the table cannot
	// grow without bound. Accepted/revoked rows are kept as an audit trail.
	DeleteExpiredInvites(ctx context.Context) error
}

// GenerateInviteToken returns a freshly minted (raw, hash) pair. Same
// shape as GenerateVerifyToken so the email-link UX is uniform: 32
// random bytes, URL-safe base64, sha256-hex hash for storage.
func GenerateInviteToken() (raw, hash string, err error) {
	return GenerateVerifyToken()
}

// HashInviteToken returns the lowercase-hex sha256 of a raw invite
// token. Alias for HashVerifyToken so callers reading the invite code
// path do not have to cross into the verify package.
func HashInviteToken(raw string) string {
	return HashVerifyToken(raw)
}

// SendInviteEmail mints a token, persists the hash via CreateInvite, and
// dispatches the invite email. Mirrors SendResetEmail in shape but uses
// the accept-invite link path and the 7-day TTL so the flows cannot be
// confused at the call sites. A mailer failure surfaces verbatim to the
// caller so the admin handler can flash a meaningful message; the invite
// row is still committed so a future resend (slice 2) can run
// independently of SMTP availability.
func SendInviteEmail(
	ctx context.Context,
	invites InviteStore,
	sender VerifyEmailSender,
	baseURL, recipient, note string,
	invitedByPlayerID int64,
	now time.Time,
) error {
	raw, hash, err := GenerateInviteToken()
	if err != nil {
		return err
	}
	link, err := buildInviteLink(baseURL, raw)
	if err != nil {
		return err
	}
	expiresAt := now.Add(InviteTokenTTL)
	if storeErr := invites.CreateInvite(ctx, recipient, hash, note, invitedByPlayerID, expiresAt); storeErr != nil {
		return fmt.Errorf("invite: store: %w", storeErr)
	}
	msg := mailer.Message{
		To:      recipient,
		Subject: "You are invited to Top Banana",
		Body:    inviteEmailBody(link),
		Kind:    mailer.KindInvite,
	}
	if sendErr := sender.Send(ctx, msg); sendErr != nil {
		return fmt.Errorf("invite: send: %w", sendErr)
	}

	return nil
}

// buildInviteLink composes the absolute accept-invite URL. Same
// validation as buildResetLink: fail loudly when BASE_URL is missing or
// shape-malformed so a misconfigured deploy does not silently produce a
// link the recipient cannot click.
func buildInviteLink(baseURL, rawToken string) (string, error) {
	if baseURL == "" {
		return "", errVerifyBaseURLEmpty
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invite: parse base url %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: %q", errVerifyBaseURLInvalid, baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/accept-invite"
	q := u.Query()
	q.Set("token", rawToken)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// inviteEmailBody is the plain-text body of the invite email. Mirrors
// the reset/verify body shape so the channels read consistently.
func inviteEmailBody(link string) string {
	return "You have been invited to join Top Banana.\n\n" +
		"Click the link below to pick a username and password and set up your account:\n\n" +
		link + "\n\n" +
		"This link is valid for 7 days. If you were not expecting this invite,\n" +
		"you can ignore this email.\n"
}
