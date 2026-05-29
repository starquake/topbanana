// Package mailer wraps SMTP for the rest of the codebase. When SMTP is
// not configured the server constructs a no-op mailer that returns
// [ErrNotConfigured], so consumer endpoints can surface a clear "email
// is not configured" message rather than a 500.
package mailer

import (
	"context"
	"errors"
)

// Kind tags a message with the feature that produced it. The diagnostics
// ring buffer renders this verbatim in the "Recent send log" so the
// operator can scan for the kind of mail that is failing. Treated as a
// stringly typed enum - the value travels through templates - so the
// constants below define the canonical wire form.
type Kind string

// Kind tag values. A new feature that sends mail should add its tag here
// rather than minting a new ad-hoc string at the call site - the
// diagnostics page filters on Kind, so a missing constant goes
// unreported in the log instead of failing loudly.
const (
	KindVerify            Kind = "verify"
	KindReset             Kind = "reset"
	KindInvite            Kind = "invite"
	KindTest              Kind = "test"
	KindEmailChangeNotice Kind = "email_change_notice"
)

// ErrNotConfigured is the sentinel returned by the no-op mailer's Send
// method. Consumer handlers match on it with [errors.Is] and surface a
// clear "email is not configured" response instead of a generic 500.
// The diagnostics endpoint maps this to a 503 with the same message.
var ErrNotConfigured = errors.New("email is not configured on this instance")

// Per-field validation sentinels for [validateMessage]. Kept as
// package-level vars so err113 stays quiet (no inline dynamic errors)
// and so tests can match on them via [errors.Is] without parsing the
// wrap message.
var (
	errMessageMissingTo      = errors.New("mailer: message To is empty")
	errMessageMissingSubject = errors.New("mailer: message Subject is empty")
	errMessageMissingKind    = errors.New("mailer: message Kind is empty")
)

// Message is the wire-shape payload [Mailer.Send] accepts. Fields are
// deliberately minimal: To, Subject, plain-text Body, and the Kind tag.
// HTML alternatives and attachments are out of scope for #321; the
// per-feature template logic will wrap this struct when the verify /
// invite flows land.
type Message struct {
	To      string
	Subject string
	Body    string
	Kind    Kind
}

// Mailer is the interface every email-producing feature talks to. The
// no-op stub, the go-mail-backed real mailer, and the [Tester]
// ring-buffer wrapper all satisfy it.
type Mailer interface {
	// Send delivers msg synchronously. Implementations MUST return
	// the underlying SMTP error verbatim on failure (#321 design
	// decision) so the diagnostics view can show "550 mailbox
	// unavailable" or "TLS handshake failed" without translation.
	// The no-op stub returns [ErrNotConfigured].
	Send(ctx context.Context, msg Message) error
}

// SendTest is a small convenience used by the diagnostics page (#321).
// It composes a fixed-content [Message] with [KindTest] and forwards
// to m.Send so the diagnostics handler does not have to know the test
// template lives in this package. Kept as a package-level helper
// rather than a method on [Mailer] so a future Mailer implementation
// (mock, fan-out, etc.) does not have to re-implement the canned
// payload.
func SendTest(ctx context.Context, m Mailer, to string) error {
	msg := Message{
		To:      to,
		Subject: "Top Banana test email",
		Body: "This is a test email from Top Banana.\n\n" +
			"If you can read this, SMTP delivery to your address is working.\n",
		Kind: KindTest,
	}

	// Pass the error through unchanged so the diagnostics handler can
	// match on [ErrNotConfigured] / show a verbatim SMTP message via
	// [errors.Is]. Wrapping here would defeat the design decision to
	// surface the underlying error literally.
	//nolint:wrapcheck // SendTest is a thin canned-message dispatcher; wrapping would hide the verbatim SMTP error the diagnostics page renders.
	return m.Send(ctx, msg)
}

// noopMailer is the implementation used when SMTP is unconfigured. Its
// Send method always returns ErrNotConfigured so a consumer can match
// on the sentinel and surface a helpful message instead of pretending
// the send succeeded.
type noopMailer struct{}

// NewNoop returns a [Mailer] that always returns [ErrNotConfigured]
// from Send. Used by [cmd/server/app] when none of the SMTP env vars
// are populated.
func NewNoop() Mailer {
	return noopMailer{}
}

// Send on the no-op mailer validates the message first, then returns
// ErrNotConfigured. Validating first matches the smtpMailer.Send
// contract so a malformed Message produces the same per-field error
// regardless of whether SMTP is wired - callers can rely on a
// well-formed Message being the precondition that gets them past
// validation, and detect ErrNotConfigured (via [errors.Is]) to surface
// the right user-facing response on top of that.
func (noopMailer) Send(_ context.Context, msg Message) error {
	if err := validateMessage(msg); err != nil {
		return err
	}

	return ErrNotConfigured
}

// validateMessage reports the first reason msg cannot be sent. Shared
// by the real and no-op-wrapping paths so a future implementation does
// not silently accept an empty To or empty Subject. The diagnostics
// test-send endpoint pre-validates the email it constructs, so an
// error here is always programmer error and gets a wrap that names the
// offending field.
func validateMessage(msg Message) error {
	switch {
	case msg.To == "":
		return errMessageMissingTo
	case msg.Subject == "":
		return errMessageMissingSubject
	case msg.Kind == "":
		return errMessageMissingKind
	}

	return nil
}
