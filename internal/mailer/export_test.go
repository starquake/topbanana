package mailer

import (
	"context"
	"errors"
)

// errExportSMTPUnexpectedType is returned by [ExportSMTPWithDispatch]
// when [NewSMTP] returns a concrete type other than *smtpMailer.
// Defined at package scope so err113 stays quiet.
var errExportSMTPUnexpectedType = errors.New("export_test: NewSMTP returned an unexpected concrete type")

// ExportNewWithClock exposes the internal clock-injected constructor so
// the external mailer_test package can pin ring-buffer timestamps
// without exporting newWithClock from the public API.
var ExportNewWithClock = newWithClock

// ExportValidateMessage exposes the unexported validateMessage helper
// so the tests can pin its three "required field" rules without
// driving them through the SMTP path.
var ExportValidateMessage = validateMessage

// ExportSMTPWithDispatch builds an SMTP mailer that calls the supplied
// dispatch closure instead of dialing go-mail. Lets the test pin the
// per-Send timeout (via SendTimeout) without standing up a real SMTP
// server. cfg.Host/Port/From must be populated so NewSMTP's argument
// check passes; the dispatch swap happens on the concrete type before
// it is returned.
func ExportSMTPWithDispatch(
	cfg SMTPConfig, dispatch func(ctx context.Context, msg Message) error,
) (Mailer, error) {
	m, err := NewSMTP(cfg)
	if err != nil {
		return nil, err
	}
	s, ok := m.(*smtpMailer)
	if !ok {
		// NewSMTP returns *smtpMailer on the success path; an
		// unexpected concrete type means the constructor's contract
		// changed and the dispatch swap will not take effect.
		return nil, errExportSMTPUnexpectedType
	}
	s.dispatch = dispatch

	return m, nil
}
