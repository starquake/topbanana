package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

// Verify-email catalog keys.
const (
	emailVerifySubjectKey       locale.MessageID = "email.verify.subject"
	emailVerifyBodyKey          locale.MessageID = "email.verify.body"
	emailChangeVerifySubjectKey locale.MessageID = "email.emailChangeVerify.subject"
	emailChangeVerifyBodyKey    locale.MessageID = "email.emailChangeVerify.body"
)

// VerifyTokenTTL is the lifetime of a freshly minted verify-email link.
// Long enough that a recipient who opens the mail next morning still
// finds it live; short enough that a leaked mailbox stops being a path
// into the account within a day.
const VerifyTokenTTL = 24 * time.Hour

// ErrVerifyTokenInvalid is returned by ConsumeVerifyToken when the
// supplied token does not match a live row. Distinct from
// ErrVerifyTokenAlreadyUsed so the handler can render an "already
// verified" page instead of a generic "this link is no longer valid".
var ErrVerifyTokenInvalid = errors.New("verify token invalid")

// ErrVerifyTokenAlreadyUsed is returned by ConsumeVerifyToken when the
// supplied token was already consumed. The caller renders an "already
// verified" landing page rather than treating the duplicate click as
// an error - the second click is a normal user action (mail client
// pre-fetched the link, browser reload, etc.).
var ErrVerifyTokenAlreadyUsed = errors.New("verify token already used")

// errVerifyBaseURLEmpty / errVerifyBaseURLInvalid back the buildVerifyLink
// validation errors. Sentinel so callers can [errors.Is] them without
// string-matching the wrap message.
var (
	errVerifyBaseURLEmpty   = errors.New("verify token: BASE_URL is empty")
	errVerifyBaseURLInvalid = errors.New("verify token: BASE_URL is invalid")
)

// verifyTokenBytes is the random-bytes length of a freshly minted
// token. 32 bytes (256 bits) is well above the brute-force threshold
// and base64-encodes to a comfortable URL length.
const verifyTokenBytes = 32

// VerifyTokenStore persists email-verify tokens. Implemented by
// store.PlayerStore against the real DB; the interface lives here so
// the auth package can drive the verify flow without a direct import
// of the storage layer.
type VerifyTokenStore interface {
	// CreateVerifyToken records the sha256 hex of a freshly minted token.
	// The raw token is never stored - a DB leak should not be replayable.
	// pendingEmail carries the new address an in-session email-change
	// request (#497) wants to switch to; "" for register-time and
	// resend rows, in which case the consume path keeps the existing
	// behaviour and only stamps email_verified_at.
	CreateVerifyToken(ctx context.Context, tokenHash string, playerID int64,
		expiresAt time.Time, pendingEmail string) error
	// ConsumeVerifyToken atomically marks the row consumed and applies
	// the verified-email side effect in the same transaction. For
	// register/resend rows (pendingEmail empty at create time) that
	// means stamping email_verified_at when currently NULL. For
	// email-change rows (pendingEmail non-empty) it instead swaps
	// players.email to the pending address, re-stamps email_verified_at,
	// and bumps session_version so every other live cookie for this
	// account is invalidated by the swap. Returns the player id on
	// success, ErrVerifyTokenAlreadyUsed when the row exists but was
	// already consumed (player id is still returned so the handler can
	// detect a session belonging to a different player),
	// ErrEmailTaken when the email-change branch hits the UNIQUE
	// constraint on players.email (someone else claimed it between
	// send and click), and ErrVerifyTokenInvalid when no live row
	// matches (never existed, or expired; player id is 0 in this case).
	ConsumeVerifyToken(ctx context.Context, tokenHash string) (int64, error)
	// DeleteExpiredVerifyTokens removes rows whose expires_at has
	// passed. Called from the startup sweep so the table cannot grow
	// without bound on a long-running deploy.
	DeleteExpiredVerifyTokens(ctx context.Context) error
}

// VerifyEmailSender is the slice of mailer.Mailer the verify-email
// flow uses. Narrow interface so tests don't need to spin up a full
// SMTP server.
type VerifyEmailSender interface {
	Send(ctx context.Context, msg mailer.Message) error
}

// GenerateVerifyToken returns a freshly minted (raw, hash) pair. The
// raw token is the URL-safe base64 encoding of 32 random bytes from
// crypto/rand; the hash is its lowercase-hex sha256. Pass the raw
// token to the email body and the hash to the store.
func GenerateVerifyToken() (raw, hash string, err error) {
	buf := make([]byte, verifyTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("verify token: read randomness: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)

	return raw, HashVerifyToken(raw), nil
}

// HashVerifyToken returns the lowercase-hex sha256 of a raw token.
// The encoded form must match what GenerateVerifyToken produced or the
// store lookup will miss.
func HashVerifyToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))

	return hex.EncodeToString(sum[:])
}

// SendVerifyEmail mints a token, persists the hash, and dispatches
// the verification email. baseURL is the absolute origin used to build
// the link (e.g. "https://topbanana.example"); the verify endpoint
// path is appended here so callers do not have to know it. A mailer
// failure surfaces verbatim to the caller; the token row is still
// committed so a future "resend" flow can run independently of SMTP
// availability.
func SendVerifyEmail(
	ctx context.Context,
	tokens VerifyTokenStore,
	sender VerifyEmailSender,
	baseURL, recipient, loc string,
	playerID int64,
	now time.Time,
) error {
	return SendVerifyEmailWithPending(ctx, tokens, sender, baseURL, recipient, "", loc, playerID, now)
}

// SendVerifyEmailWithPending is the extended variant used by the
// in-session email-change flow (#497). pendingEmail is the new address
// the player wants to switch to; the consume path picks it up off the
// token row and atomically swaps players.email. recipient is the
// address the verify link is mailed to and is intentionally separate
// from pendingEmail so a future "notify old address" flow can reuse
// the same plumbing. An empty pendingEmail mints a register-time row
// that behaves identically to today's flow.
//
//nolint:revive // argument-limit: loc is message content; the rest is irreducible mail plumbing.
func SendVerifyEmailWithPending(
	ctx context.Context,
	tokens VerifyTokenStore,
	sender VerifyEmailSender,
	baseURL, recipient, pendingEmail, loc string,
	playerID int64,
	now time.Time,
) error {
	raw, hash, err := GenerateVerifyToken()
	if err != nil {
		return err
	}
	link, err := buildVerifyLink(baseURL, raw)
	if err != nil {
		return err
	}
	expiresAt := now.Add(VerifyTokenTTL)
	if storeErr := tokens.CreateVerifyToken(ctx, hash, playerID, expiresAt, pendingEmail); storeErr != nil {
		return fmt.Errorf("verify token: store: %w", storeErr)
	}
	msg := mailer.Message{
		To:      recipient,
		Subject: verifyEmailSubject(loc, pendingEmail),
		Body:    verifyEmailBody(loc, link, pendingEmail),
		Kind:    mailer.KindVerify,
	}
	if sendErr := sender.Send(ctx, msg); sendErr != nil {
		return fmt.Errorf("verify token: send: %w", sendErr)
	}

	return nil
}

// SendVerifyEmailBestEffort dispatches the verification email and logs
// any failure rather than surfacing it. Used by the register handler
// so an SMTP outage or a mis-typed recipient does not block the
// signup from completing; the user can retry via the resend flow.
//
//nolint:revive // argument-limit: loc is message content; the rest is irreducible mail plumbing.
func SendVerifyEmailBestEffort(
	ctx context.Context,
	logger *slog.Logger,
	tokens VerifyTokenStore,
	sender VerifyEmailSender,
	baseURL, recipient, loc string,
	playerID int64,
	now time.Time,
) {
	if err := SendVerifyEmail(ctx, tokens, sender, baseURL, recipient, loc, playerID, now); err != nil {
		logger.WarnContext(ctx, "verify email dispatch failed",
			slog.Int64("player_id", playerID),
			slog.String("to", recipient),
			slog.Any("err", err),
		)
	}
}

// buildVerifyLink composes the absolute verify URL. Validates baseURL
// up front so a misconfigured BASE_URL fails the send loudly instead
// of silently producing a link the user cannot click.
func buildVerifyLink(baseURL, rawToken string) (string, error) {
	if baseURL == "" {
		return "", errVerifyBaseURLEmpty
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("verify token: parse base url %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: %q", errVerifyBaseURLInvalid, baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/verify-email"
	q := u.Query()
	q.Set("token", rawToken)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// verifyEmailSubject returns the subject line for loc. The register-time
// and email-change variants ship separate subjects so a recipient who did
// not initiate the change spots it before opening the message.
func verifyEmailSubject(loc, pendingEmail string) string {
	if pendingEmail == "" {
		return locale.Translate(loc, emailVerifySubjectKey)
	}

	return locale.Translate(loc, emailChangeVerifySubjectKey)
}

// verifyEmailBody is the plain-text body of the verification email for
// loc. When pendingEmail is set, the body explains that clicking the link
// switches the account's address to that value, so a recipient who did not
// request the change can stop and ignore the message.
func verifyEmailBody(loc, link, pendingEmail string) string {
	if pendingEmail == "" {
		return locale.TranslateWith(loc, emailVerifyBodyKey, map[string]string{"link": link})
	}

	return locale.TranslateWith(loc, emailChangeVerifyBodyKey, map[string]string{
		"pendingEmail": pendingEmail,
		"link":         link,
	})
}
