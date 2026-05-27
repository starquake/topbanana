package mailer

import (
	"context"
	"sync"
	"time"
)

// LogCapacity is the bounded size of the ring buffer used by [Tester].
// Matches the "last 20 send attempts" decision in #321; exposed as a
// const so the template's empty-state copy and the buffer share one
// number.
const LogCapacity = 20

// LogEntry is one row in the diagnostics "Recent send log" table. The
// fields mirror the columns the template renders.
type LogEntry struct {
	// SentAt is the wall-clock time the Send call returned. Defaults
	// to time.Now in production; tests inject a stub clock via
	// newWithClock so the timestamp assertion is deterministic.
	SentAt time.Time
	// To, Subject, and Kind echo the Message fields so the operator
	// can scan the log without correlating ids.
	To      string
	Subject string
	Kind    Kind
	// Err is the verbatim error from the wrapped Mailer's Send. Empty
	// string means success. The template renders this in full when
	// non-empty (#321 design decision: never hide the SMTP error).
	Err string
}

// Tester wraps a [Mailer] and records every Send call into an in-memory
// ring buffer. The diagnostics page (#321) shows the buffer's contents;
// the wrapper is also placed in front of the verify/reset/invite paths
// so the same log records all outgoing mail, not just the test-send
// button. Bounded - oldest entry is overwritten when the buffer is full.
//
// Safe for concurrent use: every public method takes t.mu so callers
// can drive Send / Recent / Count from multiple goroutines (the
// concurrent-send test exercises exactly this) without external
// synchronisation.
type Tester struct {
	inner Mailer
	now   func() time.Time

	mu    sync.Mutex
	log   []LogEntry
	count int
}

// NewTester wraps inner in a [Tester] that records every Send into the
// ring buffer. inner can be either the SMTP mailer or the no-op stub;
// either way the diagnostics log reflects what was tried.
func NewTester(inner Mailer) *Tester {
	return newWithClock(inner, time.Now)
}

// newWithClock is the internal constructor that lets tests inject a
// deterministic clock. Exported through export_test.go as
// [ExportNewWithClock] so the ring-buffer + timestamp tests do not
// need a separate package-internal seam.
func newWithClock(inner Mailer, now func() time.Time) *Tester {
	return &Tester{
		inner: inner,
		now:   now,
		log:   make([]LogEntry, 0, LogCapacity),
	}
}

// Send forwards to the wrapped mailer and records the outcome in the
// ring buffer. The recorded error is the verbatim string returned by
// inner.Send - the diagnostics page renders it without translation so
// the operator can debug SMTP issues directly.
//
//nolint:wrapcheck // Tester is a pass-through wrapper; wrapping would hide the verbatim SMTP / ErrNotConfigured error the diagnostics page surfaces literally.
func (t *Tester) Send(ctx context.Context, msg Message) error {
	err := t.inner.Send(ctx, msg)

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	// SentAt is stamped inside record under the mutex so two
	// concurrent senders can never disagree on buffer order versus
	// timestamp order: the goroutine that wins the lock is the one
	// whose clock read lands first.
	t.record(msg.To, msg.Subject, msg.Kind, errStr)

	return err
}

// Recent returns the last n entries in the ring buffer, newest first.
// n is clamped to the buffer size - asking for more than what is
// available returns whatever is there. The returned slice is a fresh
// copy; mutating it does not affect the buffer.
func (t *Tester) Recent(n int) []LogEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	if n <= 0 || len(t.log) == 0 {
		return nil
	}
	if n > len(t.log) {
		n = len(t.log)
	}

	out := make([]LogEntry, n)
	// log is stored oldest-first; the diagnostics page wants
	// newest-first so the most recent attempt is at the top. Reverse
	// while copying.
	for i := range n {
		out[i] = t.log[len(t.log)-1-i]
	}

	return out
}

// Count returns the total number of Send calls recorded since the
// Tester was created. Used by tests to assert "the wrapper saw N
// calls" without coupling to the bounded log length.
func (t *Tester) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.count
}

// record slots a new entry into the ring buffer, overwriting the
// oldest row when the buffer is at capacity. Acquires t.mu for the
// duration so concurrent Send calls do not race on len(t.log) /
// append. SentAt is read inside the lock so two concurrent senders
// cannot disagree on buffer order versus timestamp order - the
// goroutine that wins the lock is the one whose clock read lands
// first.
func (t *Tester) record(to, subject string, kind Kind, errStr string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry := LogEntry{
		SentAt:  t.now(),
		To:      to,
		Subject: subject,
		Kind:    kind,
		Err:     errStr,
	}

	if len(t.log) < LogCapacity {
		t.log = append(t.log, entry)
		t.count++

		return
	}
	// Shift everything down by one and append. A real circular buffer
	// would be cheaper but n=20 makes the asymptotic argument moot
	// and the slice-based form keeps Recent's reverse copy obvious.
	copy(t.log, t.log[1:])
	t.log[len(t.log)-1] = entry
	t.count++
}
