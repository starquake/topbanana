package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

// Reset-email catalog keys.
const (
	emailResetSubjectKey locale.MessageID = "email.reset.subject"
	emailResetBodyKey    locale.MessageID = "email.reset.body"
)

// ResetTokenTTL is the lifetime of a forgot-password link. Shorter than
// the verify TTL: a reset token grants password rotation, so the
// blast-radius of a leaked link is higher and the convenience of a
// long-lived link is lower (users typically click within minutes).
const ResetTokenTTL = 30 * time.Minute

// ErrResetTokenInvalid is returned by ConsumeResetToken when the
// supplied token does not match a live row (never existed, expired,
// or already consumed). Reset tokens are single-use by design, so
// there is no "already consumed" distinction surfaced to the caller -
// the safer UX is to render a single "this link is no longer valid"
// page and let the user request a new one.
var ErrResetTokenInvalid = errors.New("reset token invalid")

// ResetTokenStore persists password-reset tokens. Implemented by
// store.PlayerStore against the real DB; the interface lives here so
// the auth package can drive the forgot-password flow without a direct
// import of the storage layer.
type ResetTokenStore interface {
	// CreateResetToken records the sha256 hex of a freshly minted
	// token. The raw token is never stored - a DB leak should not be
	// replayable.
	CreateResetToken(ctx context.Context, tokenHash string, playerID int64, expiresAt time.Time) error
	// LookupResetToken peeks at the row without consuming it. Returns
	// the owning player id and a bool that is true iff the row exists,
	// is unconsumed, and is unexpired. Used by the GET handler to
	// short-circuit the form render for already-dead tokens so the
	// user is not asked to type a password the POST will reject. Never
	// a security boundary: the atomic consume on POST is what enforces
	// single-use; this peek only gates the render path. Returns no
	// error when the row is simply missing (live = false, player id 0).
	LookupResetToken(ctx context.Context, tokenHash string) (playerID int64, live bool, err error)
	// ConsumeResetToken atomically marks the row consumed, rotates the
	// player's password_hash, and bumps session_version in the same
	// transaction. Returns the player id on success and
	// ErrResetTokenInvalid when no live row matches. The session_version
	// bump invalidates every other in-flight cookie the moment the
	// transaction commits (#112).
	ConsumeResetToken(ctx context.Context, tokenHash, newPasswordHash string) (int64, error)
	// DeleteExpiredResetTokens removes rows whose expires_at has
	// passed. Called from the startup sweep so the table cannot grow
	// without bound on a long-running deploy.
	DeleteExpiredResetTokens(ctx context.Context) error
}

// GenerateResetToken returns a freshly minted (raw, hash) pair. Same
// shape as GenerateVerifyToken so the email-link UX is uniform: 32
// random bytes, URL-safe base64, sha256-hex hash for storage.
func GenerateResetToken() (raw, hash string, err error) {
	return GenerateVerifyToken()
}

// HashResetToken returns the lowercase-hex sha256 of a raw reset
// token. Alias for HashVerifyToken so callers reading the reset code
// path do not have to cross into the verify package.
func HashResetToken(raw string) string {
	return HashVerifyToken(raw)
}

// SendResetEmail mints a token, persists the hash, and dispatches the
// forgot-password email. Mirrors SendVerifyEmail in shape but uses the
// reset link path and the shorter TTL so the two flows cannot be
// confused at the call sites. A mailer failure surfaces verbatim to
// the caller so the forgot-password handler can flash a meaningful
// (but still account-existence-opaque) message.
func SendResetEmail(
	ctx context.Context,
	tokens ResetTokenStore,
	sender VerifyEmailSender,
	baseURL, recipient, loc string,
	playerID int64,
	now time.Time,
) error {
	raw, hash, err := GenerateResetToken()
	if err != nil {
		return err
	}
	link, err := buildResetLink(baseURL, raw)
	if err != nil {
		return err
	}
	expiresAt := now.Add(ResetTokenTTL)
	if storeErr := tokens.CreateResetToken(ctx, hash, playerID, expiresAt); storeErr != nil {
		return fmt.Errorf("reset token: store: %w", storeErr)
	}
	msg := mailer.Message{
		To:      recipient,
		Subject: locale.Translate(loc, emailResetSubjectKey),
		Body:    resetEmailBody(loc, link),
		Kind:    mailer.KindReset,
	}
	if sendErr := sender.Send(ctx, msg); sendErr != nil {
		return fmt.Errorf("reset token: send: %w", sendErr)
	}

	return nil
}

// buildResetLink composes the absolute reset URL. Same validation as
// buildVerifyLink: fail loudly when BASE_URL is missing or
// shape-malformed so a misconfigured deploy does not silently produce
// a link the user cannot click.
func buildResetLink(baseURL, rawToken string) (string, error) {
	if baseURL == "" {
		return "", errVerifyBaseURLEmpty
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("reset token: parse base url %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: %q", errVerifyBaseURLInvalid, baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/reset-password"
	q := u.Query()
	q.Set("token", rawToken)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// resetEmailBody is the plain-text body of the reset email for loc.
func resetEmailBody(loc, link string) string {
	return locale.TranslateWith(loc, emailResetBodyKey, map[string]string{"link": link})
}
