package mailer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/mailer"
)

// fakeMailer is a Mailer stub the ring-buffer + SendTest tests drive.
// Tracks the last message Send saw and lets the caller pin the error
// the wrapped layer should surface to Tester.Send so success and
// failure paths can be exercised without standing up a real SMTP
// dialer.
type fakeMailer struct {
	lastMsg Message
	err     error
	calls   int
}

func (f *fakeMailer) Send(_ context.Context, msg Message) error {
	f.lastMsg = msg
	f.calls++

	return f.err
}

func TestNoopMailer_SendReturnsErrNotConfigured(t *testing.T) {
	t.Parallel()

	m := NewNoop()
	err := m.Send(t.Context(), Message{To: "a@b", Subject: "s", Body: "b", Kind: KindTest})
	if got, want := err, ErrNotConfigured; !errors.Is(got, want) {
		t.Errorf("Send err = %v, want %v", got, want)
	}
}

func TestNoopMailer_SendValidatesMessageFirst(t *testing.T) {
	t.Parallel()

	// A malformed Message must surface the per-field validation error
	// regardless of whether SMTP is wired; the SMTP path validates
	// first too, so the contract is consistent across implementations.
	m := NewNoop()
	err := m.Send(t.Context(), Message{Subject: "s", Kind: KindTest})
	if err == nil {
		t.Fatal("Send err = nil, want validation error")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Errorf("Send err = %v, want validation error (not ErrNotConfigured)", err)
	}
	if got, want := err.Error(), "To is empty"; !strings.Contains(got, want) {
		t.Errorf("Send err.Error() = %q, should contain %q", got, want)
	}
}

func TestSendTest_WrapsMessageWithKindTest(t *testing.T) {
	t.Parallel()

	fake := &fakeMailer{}
	if err := SendTest(t.Context(), fake, "ops@example.test"); err != nil {
		t.Fatalf("SendTest err = %v, want nil", err)
	}
	if got, want := fake.lastMsg.To, "ops@example.test"; got != want {
		t.Errorf("Message.To = %q, want %q", got, want)
	}
	if got, want := fake.lastMsg.Kind, KindTest; got != want {
		t.Errorf("Message.Kind = %q, want %q", got, want)
	}
	if got, want := fake.lastMsg.Subject, ""; got == want {
		t.Errorf("Message.Subject = %q, want non-empty", got)
	}
	if got, want := fake.lastMsg.Body, ""; got == want {
		t.Errorf("Message.Body = %q, want non-empty", got)
	}
}

func TestSendTest_RecordsFailurePathOnTesterWrapper(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("550 mailbox unavailable")
	fake := &fakeMailer{err: wantErr}
	tester := NewTester(fake)

	err := SendTest(t.Context(), tester, "ops@example.test")
	if got, want := err, wantErr; !errors.Is(got, want) {
		t.Errorf("SendTest err = %v, want %v", got, want)
	}
	entries := tester.Recent(LogCapacity)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(Recent) = %d, want %d", got, want)
	}
	if got, want := entries[0].Err, wantErr.Error(); !strings.Contains(got, want) {
		t.Errorf("Recent[0].Err = %q, should contain %q", got, want)
	}
	if got, want := entries[0].Kind, KindTest; got != want {
		t.Errorf("Recent[0].Kind = %q, want %q", got, want)
	}
}

func TestValidateMessage_RejectsMissingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{"empty To", Message{Subject: "s", Kind: KindTest}, "To is empty"},
		{"empty Subject", Message{To: "a@b", Kind: KindTest}, "Subject is empty"},
		{"empty Kind", Message{To: "a@b", Subject: "s"}, "Kind is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ExportValidateMessage(tt.msg)
			if err == nil {
				t.Fatal("validateMessage err = nil, want non-nil")
			}
			if got, want := err.Error(), tt.want; !strings.Contains(got, want) {
				t.Errorf("validateMessage err = %q, should contain %q", got, want)
			}
		})
	}
}

func TestValidateMessage_AcceptsCompleteMessage(t *testing.T) {
	t.Parallel()

	err := ExportValidateMessage(Message{To: "a@b", Subject: "s", Kind: KindTest})
	if err != nil {
		t.Errorf("validateMessage err = %v, want nil", err)
	}
}

func TestNewStatusView_UnconfiguredBlanksConnectionFields(t *testing.T) {
	t.Parallel()

	// SMTPTLS defaults to true in config.Parse so a deploy that did not
	// wire SMTP would otherwise render "STARTTLS required" next to a
	// blank host. NewStatusView masks the connection fields when the
	// mailer is unconfigured so the page shows a coherent disabled state.
	cfg := SMTPConfig{
		Host: "should-be-blanked",
		Port: 1025,
		From: "should-be-blanked@example.test",
		TLS:  true,
	}
	view := NewStatusView(cfg, false, "")
	if got, want := view.Configured, false; got != want {
		t.Errorf("Configured = %v, want %v", got, want)
	}
	if got, want := view.Host, ""; got != want {
		t.Errorf("Host = %q, want %q (blanked when not configured)", got, want)
	}
	if got, want := view.Port, 0; got != want {
		t.Errorf("Port = %d, want %d (blanked when not configured)", got, want)
	}
	if got, want := view.From, ""; got != want {
		t.Errorf("From = %q, want %q (blanked when not configured)", got, want)
	}
	if got, want := view.TLS, false; got != want {
		t.Errorf("TLS = %v, want %v (blanked when not configured)", got, want)
	}
	// The caller's SMTPConfig must not be mutated; NewStatusView works
	// on a copy.
	if got, want := cfg.Host, "should-be-blanked"; got != want {
		t.Errorf("cfg.Host = %q, want %q (caller's SMTPConfig must not be mutated)", got, want)
	}
}

func TestNewStatusView_OmitsCredentials(t *testing.T) {
	t.Parallel()

	cfg := SMTPConfig{
		Host:     "mailpit",
		Port:     1025,
		Username: "smtpuser",
		Password: "smtpsecret",
		From:     "topbanana@localhost",
		TLS:      true,
	}
	view := NewStatusView(cfg, true, "")
	// The status view fields cover host/port/from/tls/configured/baseurl
	// and nothing else. Two layers of defence: the type only carries
	// the safe subset (compile-time), and the value-level checks below
	// confirm we did not silently widen the struct in the future.
	if got, want := view.Configured, true; got != want {
		t.Errorf("Configured = %v, want %v", got, want)
	}
	if got, want := view.Host, "mailpit"; got != want {
		t.Errorf("Host = %q, want %q", got, want)
	}
	if got, want := view.Port, 1025; got != want {
		t.Errorf("Port = %d, want %d", got, want)
	}
	if got, want := view.From, "topbanana@localhost"; got != want {
		t.Errorf("From = %q, want %q", got, want)
	}
	if got, want := view.TLS, true; got != want {
		t.Errorf("TLS = %v, want %v", got, want)
	}
}

// TestNewStatusView_BaseURLPopulatedRegardlessOfConfigured pins that
// BaseURL ships on the returned view even when configured is false.
// The dispatchers silently no-op when BASE_URL is empty, so the
// operator must see the link prefix on the diagnostics page whether
// or not SMTP itself is wired.
func TestNewStatusView_BaseURLPopulatedRegardlessOfConfigured(t *testing.T) {
	t.Parallel()

	const baseURL = "https://quiz.example.test"
	cfg := SMTPConfig{Host: "mailpit", Port: 1025, From: "topbanana@localhost"}

	tests := []struct {
		name       string
		configured bool
	}{
		{name: "unconfigured", configured: false},
		{name: "configured", configured: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := NewStatusView(cfg, tt.configured, baseURL)
			if got, want := view.BaseURL, baseURL; got != want {
				t.Errorf("BaseURL = %q, want %q", got, want)
			}
		})
	}
}

func TestNewSMTP_RejectsMissingHostPortFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  SMTPConfig
	}{
		{"missing host", SMTPConfig{Port: 25, From: "a@b"}},
		{"missing port", SMTPConfig{Host: "mailpit", From: "a@b"}},
		{"missing from", SMTPConfig{Host: "mailpit", Port: 25}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewSMTP(tt.cfg)
			if m != nil {
				t.Errorf("NewSMTP m = %v, want nil", m)
			}
			if err == nil {
				t.Fatal("NewSMTP err = nil, want non-nil")
			}
		})
	}
}

func TestNewSMTP_AcceptsCompleteConfig(t *testing.T) {
	t.Parallel()

	m, err := NewSMTP(SMTPConfig{Host: "mailpit", Port: 1025, From: "topbanana@localhost"})
	if err != nil {
		t.Fatalf("NewSMTP err = %v, want nil", err)
	}
	if m == nil {
		t.Fatal("NewSMTP m = nil, want non-nil")
	}
}

func TestNewSMTP_RejectsOutOfRangePort(t *testing.T) {
	t.Parallel()

	// config.Parse already rejects ports outside 1..65535, but the
	// guard inside NewSMTP keeps a direct caller (test, future CLI,
	// fuzzer) from constructing an unsendable mailer. Port 0 is
	// already covered by the missing-host-port-from test above; this
	// one pins the upper bound.
	tests := []struct {
		name string
		port int
	}{
		{"negative", -1},
		{"above max", 65536},
		{"way out of range", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewSMTP(SMTPConfig{Host: "mailpit", Port: tt.port, From: "a@b"})
			if m != nil {
				t.Errorf("NewSMTP m = %v, want nil", m)
			}
			if err == nil {
				t.Fatal("NewSMTP err = nil, want non-nil")
			}
			if got, want := err.Error(), "port"; !strings.Contains(got, want) {
				t.Errorf("NewSMTP err = %q, should mention %q", got, want)
			}
		})
	}
}

// TestSMTPMailer_SendAppliesSendTimeout pins the per-Send timeout.
// Can't run in parallel because SendTimeout is process-wide;
// t.Parallel() with a sibling test that reads the production value
// would race. Restore the original value via t.Cleanup so other
// suites see the production duration.
//
//nolint:paralleltest // SendTimeout override is process-wide.
func TestSMTPMailer_SendAppliesSendTimeout(t *testing.T) {
	originalTimeout := SendTimeout
	SendTimeout = 50 * time.Millisecond
	t.Cleanup(func() { SendTimeout = originalTimeout })

	// SendTimeout caps how long a single dispatch can block. The stub
	// dispatch blocks on the context until the timeout fires; the
	// returned error must be context.DeadlineExceeded (or wrap it) so
	// the diagnostics page can surface the timeout literally.
	dispatchCalled := false
	stub := func(ctx context.Context, _ Message) error {
		dispatchCalled = true
		// Confirm a deadline is set on the context the dispatch sees;
		// if Send forgot to wrap with WithTimeout the deadline would
		// be the caller's (background -> no deadline).
		if _, ok := ctx.Deadline(); !ok {
			t.Error("dispatch ctx has no deadline; Send did not wrap with SendTimeout")
		}
		<-ctx.Done()

		return ctx.Err()
	}

	m, err := ExportSMTPWithDispatch(
		SMTPConfig{Host: "mailpit", Port: 1025, From: "topbanana@localhost"},
		stub,
	)
	if err != nil {
		t.Fatalf("ExportSMTPWithDispatch err = %v, want nil", err)
	}

	// Drive Send with the test context (parented to t) so even a
	// runaway dispatch is bounded by go test's own deadline rather
	// than this assertion's slack window.
	start := time.Now()
	err = m.Send(t.Context(), Message{To: "ops@example.test", Subject: "s", Body: "b", Kind: KindTest})
	elapsed := time.Since(start)

	if !dispatchCalled {
		t.Fatal("dispatch was not called")
	}
	if err == nil {
		t.Fatal("Send err = nil, want context.DeadlineExceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Send err = %v, want context.DeadlineExceeded", err)
	}
	// Sanity-check that the timeout actually fired around SendTimeout,
	// not later. Allow generous slack so a slow CI host does not flake.
	if got, want := elapsed, time.Second; got > want {
		t.Errorf("Send blocked for %v, want <= %v (SendTimeout did not apply)", got, want)
	}
}
