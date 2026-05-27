package mailer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wneessen/go-mail"
)

// SendTimeout caps how long a single SMTP dispatch can block before the
// inner DialAndSendWithContext gets cancelled. The HTTP request that
// drove the diagnostics test send already has its own timeout, but a
// background-context caller (the future verify / invite paths) needs a
// floor regardless of how it got here so an unresponsive SMTP server
// cannot pin the calling goroutine indefinitely. 30 s is generous for
// a healthy dial+auth+data exchange but tight enough that an admin
// staring at the diagnostics page gets a verbatim error before they
// give up and refresh.
//
// SendTimeout is a var rather than a const so unit tests can shrink it
// to a few milliseconds and assert the timeout actually fires without
// blocking the suite for the production duration. Production callers
// should never write to it - the value is mutated at most once at
// process start by the test entrypoint.
//
//nolint:gochecknoglobals // intentional test-mutable knob; see doc above.
var SendTimeout = 30 * time.Second

// errSMTPMailerMissingHostPortFrom is the sentinel [NewSMTP] returns
// when the supplied [SMTPConfig] is missing one of the required
// host/port/from fields. Pinned at package scope so err113 stays
// quiet and tests can match on it via [errors.Is] without parsing
// the wrap message.
var errSMTPMailerMissingHostPortFrom = errors.New("smtp mailer: host, port, and from are required")

// errSMTPMailerPortOutOfRange is the sentinel [NewSMTP] returns when
// the supplied [SMTPConfig] carries a Port outside the legal TCP
// range. The env parser also rejects this in config.Parse; the guard
// here keeps a direct caller (test, future CLI, fuzzer) from
// constructing a mailer that would fail at dial time.
var errSMTPMailerPortOutOfRange = errors.New("smtp mailer: port must be in 1..65535")

// smtpMaxPort is the upper bound on a legal TCP port number. Pinned
// as a package-level const so NewSMTP's guard and any future
// validation share one number.
const smtpMaxPort = 65535

// SMTPConfig is the connection blob the SMTP-backed mailer needs. Populated
// from the [config.Config] SMTP_* fields at startup; tests pass it directly
// without going through env vars.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS toggles STARTTLS on the SMTP connection. true (the default
	// in config.Parse) requires TLS; false drops to plain SMTP, which
	// is what the local Mailpit dev setup wants.
	TLS bool
}

// smtpMailer is the [Mailer] implementation that actually talks SMTP via
// github.com/wneessen/go-mail. Wraps SMTPConfig + the configured From
// address; New constructs a fresh client per Send so the connection is
// short-lived and the diagnostics path never blocks on a previous send.
//
// dispatch is the seam that lets tests pin the per-send timeout
// without standing up an SMTP server. Production callers leave it nil
// so Send falls back to the real go-mail client; the export_test
// helper swaps in a stub that observes the context deadline.
type smtpMailer struct {
	cfg      SMTPConfig
	dispatch func(ctx context.Context, msg Message) error
}

// NewSMTP returns a [Mailer] backed by the wneessen/go-mail SMTP client.
// The client is constructed fresh on every Send so a transient broken
// connection (the SMTP server kicked us, the network dropped) does not
// require restarting the binary - same rationale that batch jobs use a
// one-shot client per dispatch.
//
// Returns an error if the cfg is missing the bare minimum (host, port,
// from) or if Port is outside the legal TCP range. [config.Parse]
// already enforces both, so a returned error here is wiring drift
// rather than a runtime knob - but the guard keeps a direct caller
// (test, future CLI, fuzzer) from constructing an unsendable mailer.
func NewSMTP(cfg SMTPConfig) (Mailer, error) {
	if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" {
		return nil, fmt.Errorf("%w (got host=%q port=%d from=%q)",
			errSMTPMailerMissingHostPortFrom, cfg.Host, cfg.Port, cfg.From)
	}
	if cfg.Port < 1 || cfg.Port > smtpMaxPort {
		return nil, fmt.Errorf("%w (got port=%d)", errSMTPMailerPortOutOfRange, cfg.Port)
	}

	return &smtpMailer{cfg: cfg}, nil
}

// Send dials the SMTP server, builds a plain-text message, and delivers
// it. Any failure on the dial, the auth handshake, or the data phase
// surfaces verbatim - the diagnostics view (#321) shows the underlying
// error on the test-send result so the operator can debug "550 mailbox
// unavailable" or "TLS handshake failed" without re-running with a
// debugger.
//
// The supplied ctx is wrapped with [SendTimeout] so a single dispatch
// cannot pin the calling goroutine indefinitely when the SMTP server
// is unresponsive. Callers that already have a tighter deadline win
// (context.WithTimeout keeps the earlier of the two), so the cap is a
// floor, not a ceiling.
func (m *smtpMailer) Send(ctx context.Context, msg Message) error {
	if err := validateMessage(msg); err != nil {
		return err
	}

	sendCtx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	if m.dispatch != nil {
		return m.dispatch(sendCtx, msg)
	}

	return m.dispatchViaGoMail(sendCtx, msg)
}

// dispatchViaGoMail is the production send path. Split from Send so
// tests can swap m.dispatch without re-implementing the per-Send
// timeout / validation guards above.
func (m *smtpMailer) dispatchViaGoMail(ctx context.Context, msg Message) error {
	gmMsg := mail.NewMsg()
	if err := gmMsg.From(m.cfg.From); err != nil {
		return fmt.Errorf("smtp mailer: set from: %w", err)
	}
	if err := gmMsg.To(msg.To); err != nil {
		return fmt.Errorf("smtp mailer: set to: %w", err)
	}
	gmMsg.Subject(msg.Subject)
	gmMsg.SetBodyString(mail.TypeTextPlain, msg.Body)

	client, err := m.newClient()
	if err != nil {
		return fmt.Errorf("smtp mailer: build client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, gmMsg); err != nil {
		return fmt.Errorf("smtp mailer: send: %w", err)
	}

	return nil
}

// newClient builds the go-mail client according to the SMTPConfig. The
// TLS toggle picks between TLSMandatory and NoTLS so the local Mailpit
// dev path (no TLS, port 1025) and the production path (STARTTLS on
// port 587) share the same constructor.
func (m *smtpMailer) newClient() (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(m.cfg.Port),
	}
	if m.cfg.TLS {
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}
	if m.cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(m.cfg.Username),
			mail.WithPassword(m.cfg.Password),
		)
	}
	client, err := mail.NewClient(m.cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}

	return client, nil
}
