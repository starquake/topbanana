package mailer_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/mailer"
)

// recordingMailer is a Mailer stub the tester tests drive: it never
// errors and just records the call count so the wrapper's pass-through
// can be verified without standing up an SMTP dialer. calls is
// atomic so the concurrent-Send test can read it from another
// goroutine without a data race.
type recordingMailer struct {
	calls atomic.Int64
}

func (r *recordingMailer) Send(_ context.Context, _ Message) error {
	r.calls.Add(1)

	return nil
}

func (r *recordingMailer) callCount() int {
	return int(r.calls.Load())
}

func TestTester_RecordsSuccessfulSend(t *testing.T) {
	t.Parallel()

	inner := &recordingMailer{}
	tester := NewTester(inner)

	err := tester.Send(t.Context(), Message{
		To:      "ops@example.test",
		Subject: "hello",
		Body:    "body",
		Kind:    KindTest,
	})
	if err != nil {
		t.Fatalf("Send err = %v, want nil", err)
	}
	if got, want := inner.callCount(), 1; got != want {
		t.Errorf("inner.callCount() = %d, want %d", got, want)
	}
	entries := tester.Recent(LogCapacity)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(Recent) = %d, want %d", got, want)
	}
	if got, want := entries[0].Err, ""; got != want {
		t.Errorf("Recent[0].Err = %q, want empty (success)", got)
	}
	if got, want := entries[0].To, "ops@example.test"; got != want {
		t.Errorf("Recent[0].To = %q, want %q", got, want)
	}
	if got, want := entries[0].Subject, "hello"; got != want {
		t.Errorf("Recent[0].Subject = %q, want %q", got, want)
	}
	if got, want := entries[0].Kind, KindTest; got != want {
		t.Errorf("Recent[0].Kind = %q, want %q", got, want)
	}
}

// errMailer is a Mailer stub that always returns the same configured
// error so the failure path through the Tester wrapper can be asserted
// verbatim.
type errMailer struct {
	err error
}

func (e *errMailer) Send(_ context.Context, _ Message) error { return e.err }

func TestTester_RecordsFailedSendWithVerbatimError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("550 mailbox unavailable")
	tester := NewTester(&errMailer{err: wantErr})

	err := tester.Send(t.Context(), Message{
		To:      "ops@example.test",
		Subject: "test",
		Body:    "body",
		Kind:    KindTest,
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("Send err = %v, want %v", err, wantErr)
	}
	entries := tester.Recent(LogCapacity)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(Recent) = %d, want %d", got, want)
	}
	if got, want := entries[0].Err, wantErr.Error(); got != want {
		t.Errorf("Recent[0].Err = %q, want %q", got, want)
	}
}

func TestTester_RecentNewestFirst(t *testing.T) {
	t.Parallel()

	clock := newSteppingClock(time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC))
	tester := ExportNewWithClock(&recordingMailer{}, clock.Now)

	sends := []string{"a@x", "b@x", "c@x"}
	for _, to := range sends {
		if err := tester.Send(t.Context(), Message{To: to, Subject: "s", Kind: KindTest}); err != nil {
			t.Fatalf("Send err = %v, want nil", err)
		}
	}

	got := tester.Recent(LogCapacity)
	if want := len(sends); len(got) != want {
		t.Fatalf("len(Recent) = %d, want %d", len(got), want)
	}
	// Newest first: "c@x" came in last and must be at index 0.
	if got, want := got[0].To, "c@x"; got != want {
		t.Errorf("Recent[0].To = %q, want %q", got, want)
	}
	if got, want := got[1].To, "b@x"; got != want {
		t.Errorf("Recent[1].To = %q, want %q", got, want)
	}
	if got, want := got[2].To, "a@x"; got != want {
		t.Errorf("Recent[2].To = %q, want %q", got, want)
	}
}

func TestTester_RecentClampsAndDefendsBuffer(t *testing.T) {
	t.Parallel()

	tester := NewTester(&recordingMailer{})
	for i := range 3 {
		_ = tester.Send(t.Context(), Message{
			To:      "x@y",
			Subject: "subject-" + string(rune('a'+i)),
			Kind:    KindTest,
		})
	}

	// Asking for more than the buffer holds returns what is there.
	if got, want := len(tester.Recent(LogCapacity)), 3; got != want {
		t.Errorf("len(Recent(cap)) = %d, want %d", got, want)
	}
	// Asking for zero or a negative value returns nil.
	if got := tester.Recent(0); got != nil {
		t.Errorf("Recent(0) = %v, want nil", got)
	}
	if got := tester.Recent(-1); got != nil {
		t.Errorf("Recent(-1) = %v, want nil", got)
	}

	// Mutating the returned slice must not affect the internal buffer.
	out := tester.Recent(2)
	out[0].To = "tampered"
	again := tester.Recent(2)
	if got, want := again[0].To, "x@y"; got != want {
		t.Errorf("Recent[0].To after caller mutated copy = %q, want %q", got, want)
	}
}

func TestTester_RingBufferWrapsAtCapacity(t *testing.T) {
	t.Parallel()

	tester := NewTester(&recordingMailer{})
	totalSends := LogCapacity + 5
	for i := range totalSends {
		if err := tester.Send(t.Context(), Message{
			To:      "x@y",
			Subject: subjectFor(i),
			Kind:    KindTest,
		}); err != nil {
			t.Fatalf("Send err = %v, want nil", err)
		}
	}

	if got, want := tester.Count(), totalSends; got != want {
		t.Errorf("Count = %d, want %d", got, want)
	}
	entries := tester.Recent(LogCapacity + 10)
	if got, want := len(entries), LogCapacity; got != want {
		t.Errorf("len(Recent) = %d, want %d (capacity)", got, want)
	}
	// The newest entry's Subject pins the iteration index; only the
	// most-recent LogCapacity Sends survive the wrap, oldest dropped.
	if got, want := entries[0].Subject, subjectFor(totalSends-1); got != want {
		t.Errorf("Recent[0].Subject = %q, want %q", got, want)
	}
	// And the oldest surviving entry is the (totalSends - LogCapacity)th
	// send: anything before that was overwritten by the ring.
	if got, want := entries[LogCapacity-1].Subject, subjectFor(totalSends-LogCapacity); got != want {
		t.Errorf("Recent[%d].Subject = %q, want %q", LogCapacity-1, got, want)
	}
}

// subjectFor returns the deterministic Subject the ring-buffer test
// stamps on its synthetic messages so the wrap-at-capacity assertion
// reads as a single source of truth.
func subjectFor(i int) string {
	return "send-" + strconv.Itoa(i)
}

// steppingClock returns a deterministic monotonic clock for the Tester
// ring-buffer tests so successive Send calls land on distinct
// timestamps without sleeping.
type steppingClock struct {
	now time.Time
}

func newSteppingClock(start time.Time) *steppingClock {
	return &steppingClock{now: start}
}

func (c *steppingClock) Now() time.Time {
	out := c.now
	c.now = c.now.Add(time.Second)

	return out
}

// monotonicClock returns a clock whose successive reads are strictly
// increasing in nanoseconds. Concurrent calls to Now go through an
// atomic add so two goroutines never observe the same timestamp;
// pinning the ConcurrentSendsKeepBufferOrdered invariant requires
// distinct stamps so the assertion is unambiguous.
type monotonicClock struct {
	start time.Time
	step  atomic.Int64
}

func (c *monotonicClock) Now() time.Time {
	return c.start.Add(time.Duration(c.step.Add(1)))
}

func TestTester_ConcurrentSendsKeepBufferOrdered(t *testing.T) {
	t.Parallel()

	// Two concurrent senders must not be able to interleave such that
	// the append order disagrees with the SentAt order. Stamping the
	// timestamp inside the mutex makes the "lock winner" the
	// "timestamp winner" so the assertion below holds.
	clock := &monotonicClock{start: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)}
	tester := ExportNewWithClock(&recordingMailer{}, clock.Now)

	// Keep the sender count within LogCapacity so every send survives
	// the ring buffer; the assertion below pins buffer-vs-timestamp
	// order, which requires the full set to be observable.
	const senders = LogCapacity
	var wg sync.WaitGroup
	wg.Add(senders)
	for i := range senders {
		go func() {
			defer wg.Done()
			_ = tester.Send(t.Context(), Message{
				To:      "x@y",
				Subject: "send-" + strconv.Itoa(i),
				Kind:    KindTest,
			})
		}()
	}
	wg.Wait()

	// Recent returns newest-first; reversed, that is the append order.
	entries := tester.Recent(senders)
	if got, want := len(entries), senders; got != want {
		t.Fatalf("len(Recent) = %d, want %d", got, want)
	}
	for i := 1; i < len(entries); i++ {
		// entries[i-1] is newer (later append) than entries[i]; its
		// SentAt must therefore be >= entries[i].SentAt.
		if entries[i-1].SentAt.Before(entries[i].SentAt) {
			t.Errorf(
				"Recent[%d].SentAt = %v is before Recent[%d].SentAt = %v; "+
					"buffer order disagrees with timestamp order",
				i-1, entries[i-1].SentAt, i, entries[i].SentAt,
			)
		}
	}
}
