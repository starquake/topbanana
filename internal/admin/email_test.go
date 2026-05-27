package admin_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/mailer"
)

// stubRecorder satisfies admin.EmailRecorder for handler tests. The
// recorded fields let each test assert what the handler dispatched
// (recipient, kind) without standing up the real Tester or SMTP path.
// observeCtx is invoked from inside Send so the detached-context test
// can read the context's live state before the handler's deferred
// cancel fires.
type stubRecorder struct {
	sendErr    error
	lastMsg    mailer.Message
	observeCtx func(context.Context)
	callCnt    int
	entries    []mailer.LogEntry
}

func (s *stubRecorder) Send(ctx context.Context, msg mailer.Message) error {
	s.lastMsg = msg
	s.callCnt++
	if s.observeCtx != nil {
		s.observeCtx(ctx)
	}

	return s.sendErr
}

func (s *stubRecorder) Recent(_ int) []mailer.LogEntry { return s.entries }

func TestEmailRateLimiter_AllowStampsAtomically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := NewEmailRateLimiterWithClock(10*time.Second, clock, nil)

	// First Allow at a fresh ip returns true and stamps the bucket
	// in the same lock acquisition; the token echoes the stamp so the
	// caller can pass it to Cancel.
	ok, _, token := l.Allow("1.2.3.4")
	if !ok {
		t.Fatal("first Allow = false, want true")
	}
	if got, want := token, now; !got.Equal(want) {
		t.Errorf("first Allow token = %v, want %v", got, want)
	}
	// Second Allow within the window returns (false, wait>0, zero
	// token); the first call's stamp already locked the bucket so the
	// request queue cannot disagree on who got through.
	if ok, wait, tok := l.Allow("1.2.3.4"); ok || wait <= 0 || !tok.IsZero() {
		t.Errorf(
			"second Allow within window = (ok=%v, wait=%v, token=%v), want (false, >0, zero)",
			ok, wait, tok,
		)
	}
	// Different IP has its own bucket.
	if ok, _, _ := l.Allow("9.9.9.9"); !ok {
		t.Error("Allow for different ip = false, want true")
	}

	// Advance the clock past the window: the original IP is allowed again.
	now = now.Add(11 * time.Second)
	if ok, _, _ := l.Allow("1.2.3.4"); !ok {
		t.Error("post-window Allow = false, want true")
	}
}

func TestEmailRateLimiter_CancelRevertsStamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := NewEmailRateLimiterWithClock(10*time.Second, clock, nil)

	// Allow stamps the bucket; Cancel with the matching token reverts
	// that stamp so the immediate retry within the window is admitted.
	ok, _, token := l.Allow("1.2.3.4")
	if !ok {
		t.Fatal("first Allow = false, want true")
	}
	l.Cancel("1.2.3.4", token)
	if ok, _, _ := l.Allow("1.2.3.4"); !ok {
		t.Error("Allow after Cancel within window = false, want true (Cancel must roll the stamp back)")
	}
	// Cancel on a fresh ip is a no-op (and must not panic).
	l.Cancel("never-stamped", time.Now())
}

func TestEmailRateLimiter_CancelLeavesNewerStampAlone(t *testing.T) {
	t.Parallel()

	// Race scenario: caller A reserves at t0, caller B re-stamps at t1
	// (clock skew, GC pause, or just a slow validation path between
	// A's Allow and Cancel), then A cancels. The token check pins B's
	// stamp so A's Cancel cannot clobber B's window.
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := NewEmailRateLimiterWithClock(10*time.Second, clock, nil)

	// A reserves the bucket and walks off to validate the recipient.
	ok, _, tokenA := l.Allow("1.2.3.4")
	if !ok {
		t.Fatal("first Allow = false, want true")
	}

	// Between A's Allow and Cancel the window expires and B reserves a
	// fresh stamp on the same ip. (Advancing the clock past the
	// window simulates the "GC pause / slow validation" gap.)
	now = now.Add(11 * time.Second)
	okB, _, tokenB := l.Allow("1.2.3.4")
	if !okB {
		t.Fatal("B's Allow = false, want true (window had expired)")
	}
	if tokenA.Equal(tokenB) {
		t.Fatalf("tokenA = %v and tokenB = %v should differ; the test relies on distinct stamps", tokenA, tokenB)
	}

	// A's bail-out arrives late and tries to refund its stamp. The
	// token check rejects the cancel: B's newer stamp stays in place.
	l.Cancel("1.2.3.4", tokenA)

	if ok, wait, _ := l.Allow("1.2.3.4"); ok || wait <= 0 {
		t.Errorf(
			"Allow after stale Cancel = (ok=%v, wait=%v), want (false, >0): B's stamp must still gate the bucket",
			ok, wait,
		)
	}
}

func TestEmailRateLimiter_ConcurrentAllowAdmitsExactlyOne(t *testing.T) {
	t.Parallel()

	// Two goroutines calling Allow on the same ip in parallel must not
	// both observe "allowed": the original Peek/Record split made the
	// check non-atomic so this invariant could break under contention.
	// Stamping the bucket inside the same lock acquisition pins it.
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := NewEmailRateLimiterWithClock(10*time.Second, clock, nil)

	const goroutines = 32
	var admitted atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			if ok, _, _ := l.Allow("1.2.3.4"); ok {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got, want := admitted.Load(), int64(1); got != want {
		t.Errorf("concurrent Allow admitted %d goroutines, want %d", got, want)
	}
}

func TestEmailRateLimiter_PrunesStaleEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l := NewEmailRateLimiterWithClock(10*time.Second, clock, nil)

	if ok, _, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("first Allow = false, want true")
	}
	if ok, _, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("second Allow = false, want true")
	}
	if got, want := EmailRateLimiterEntryCount(l), 2; got != want {
		t.Fatalf("entries after first Allow pair = %d, want %d", got, want)
	}

	// Advance past 2 * window so both entries qualify as stale; the
	// next operation prunes them in passing.
	now = now.Add(25 * time.Second)
	if ok, _, _ := l.Allow("9.9.9.9"); !ok {
		t.Fatal("post-prune Allow = false, want true")
	}
	if got, want := EmailRateLimiterEntryCount(l), 1; got != want {
		t.Errorf("entries after prune = %d, want %d", got, want)
	}
}

func TestHandleEmailGet_RendersStatusAndLog(t *testing.T) {
	t.Parallel()

	status := mailer.StatusView{
		Configured: true,
		Host:       "mailpit",
		Port:       1025,
		From:       "topbanana@localhost",
		TLS:        false,
	}
	recorder := &stubRecorder{
		entries: []mailer.LogEntry{
			{
				SentAt:  time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
				To:      "ops@example.test",
				Subject: "Top Banana test email",
				Kind:    mailer.KindTest,
			},
		},
	}

	body := renderGET(t, status, recorder)

	// Host / port / from must appear in the status panel.
	if got, want := body, "mailpit"; !strings.Contains(got, want) {
		t.Errorf("body should contain Host %q", want)
	}
	if got, want := body, "1025"; !strings.Contains(got, want) {
		t.Errorf("body should contain Port %q", want)
	}
	if got, want := body, "topbanana@localhost"; !strings.Contains(got, want) {
		t.Errorf("body should contain From %q", want)
	}
	// Ring buffer entry surfaces.
	if got, want := body, "ops@example.test"; !strings.Contains(got, want) {
		t.Errorf("body should contain log recipient %q", want)
	}
	if got, want := body, "Top Banana test email"; !strings.Contains(got, want) {
		t.Errorf("body should contain log subject %q", want)
	}
}

func TestHandleEmailGet_NeverExposesCredentials(t *testing.T) {
	t.Parallel()

	// StatusView intentionally lacks Username / Password fields, but
	// pin the rendered body just in case a future change widens the
	// struct: a credential must never reach the template.
	status := mailer.StatusView{
		Configured: true,
		Host:       "mailpit",
		Port:       1025,
		From:       "topbanana@localhost",
		TLS:        true,
	}
	recorder := &stubRecorder{}

	body := renderGET(t, status, recorder)

	for _, secret := range []string{"super-secret-password", "smtpuser", "SMTP_PASSWORD"} {
		if strings.Contains(body, secret) {
			t.Errorf("body must not contain credential token %q", secret)
		}
	}
}

func TestHandleEmailTest_NotConfiguredFlashesError(t *testing.T) {
	t.Parallel()

	recorder := &stubRecorder{sendErr: mailer.ErrNotConfigured}
	limiter := NewEmailRateLimiterWithClock(
		10*time.Second,
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
		nil,
	)

	rr := postEmailTest(t, recorder, limiter, "ops@example.test")

	assertRedirectToAdminEmail(t, rr)
	kind, msg, _ := decodeFlashFromRecorder(t, rr)
	if got, want := kind, FlashError; got != want {
		t.Errorf("flash kind = %q, want %q", got, want)
	}
	if got, want := msg, "Email is not configured"; !strings.Contains(got, want) {
		t.Errorf("flash msg = %q, should contain %q", got, want)
	}
	if got, want := recorder.callCnt, 1; got != want {
		t.Errorf("recorder.callCnt = %d, want %d", got, want)
	}
	if got, want := recorder.lastMsg.Kind, mailer.KindTest; got != want {
		t.Errorf("recorder.lastMsg.Kind = %q, want %q", got, want)
	}
}

func TestHandleEmailTest_RateLimitsRepeatedSends(t *testing.T) {
	t.Parallel()

	recorder := &stubRecorder{}
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	limiter := NewEmailRateLimiterWithClock(10*time.Second, func() time.Time { return now }, nil)

	// First click is admitted: 303 to /admin/email with a success flash.
	rr1 := postEmailTest(t, recorder, limiter, "ops@example.test")
	assertRedirectToAdminEmail(t, rr1)
	kind1, _, _ := decodeFlashFromRecorder(t, rr1)
	if got, want := kind1, FlashNotice; got != want {
		t.Errorf("first POST flash kind = %q, want %q", got, want)
	}

	// Second click from the same IP at the same instant is denied.
	rr2 := postEmailTest(t, recorder, limiter, "ops@example.test")
	assertRedirectToAdminEmail(t, rr2)
	kind2, msg2, wait2 := decodeFlashFromRecorder(t, rr2)
	if got, want := kind2, FlashError; got != want {
		t.Errorf("second POST flash kind = %q, want %q", got, want)
	}
	if got, want := msg2, "Slow down"; !strings.Contains(got, want) {
		t.Errorf("second POST flash msg = %q, should contain %q", got, want)
	}
	if wait2 <= 0 {
		t.Errorf("second POST flash wait = %d, want > 0", wait2)
	}
	// Retry-After alongside the banner: scripted callers honour the
	// header even though the response is 303, not 429.
	if got := rr2.Header().Get("Retry-After"); got == "" {
		t.Errorf("Retry-After = %q, want non-empty", got)
	}
}

func TestHandleEmailTest_InvalidRecipientFlashesError(t *testing.T) {
	t.Parallel()

	recorder := &stubRecorder{}
	limiter := NewEmailRateLimiterWithClock(
		10*time.Second,
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
		nil,
	)

	rr := postEmailTest(t, recorder, limiter, "not-an-email")

	assertRedirectToAdminEmail(t, rr)
	kind, msg, _ := decodeFlashFromRecorder(t, rr)
	if got, want := kind, FlashError; got != want {
		t.Errorf("flash kind = %q, want %q", got, want)
	}
	if got, want := msg, "Recipient is not a valid email address"; !strings.Contains(got, want) {
		t.Errorf("flash msg = %q, should contain %q", got, want)
	}
	// The handler must NOT dispatch to the admin's own address when the
	// form value is set-but-invalid; that would surprise the operator
	// (they clearly meant the form value).
	if got, want := recorder.callCnt, 0; got != want {
		t.Errorf("recorder.callCnt = %d, want %d (no fallback dispatch)", got, want)
	}
}

func TestHandleEmailTest_InvalidRecipientDoesNotConsumeBucket(t *testing.T) {
	t.Parallel()

	recorder := &stubRecorder{}
	limiter := NewEmailRateLimiterWithClock(
		10*time.Second,
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
		nil,
	)

	// First POST is rejected on validation; the bucket should NOT be
	// consumed, so the immediate retry with a good address still goes
	// through (success flash, not a rate-limit flash).
	rr1 := postEmailTest(t, recorder, limiter, "not-an-email")
	if kind, _, _ := decodeFlashFromRecorder(t, rr1); kind != FlashError {
		t.Errorf("first POST flash kind = %q, want %q", kind, FlashError)
	}
	rr2 := postEmailTest(t, recorder, limiter, "ops@example.test")
	kind, msg, _ := decodeFlashFromRecorder(t, rr2)
	if got, want := kind, FlashNotice; got != want {
		t.Errorf("second POST flash kind = %q, want %q (validation failure must not burn the bucket)", got, want)
	}
	if got, want := msg, "Test email sent"; !strings.Contains(got, want) {
		t.Errorf("second POST flash msg = %q, should contain %q", got, want)
	}
}

// TestHandleEmailTest_DetachesRequestContext pins the #472 fix: the
// send must run against a context detached from r.Context() so a
// closed-tab cancellation does not surface as "context canceled" in
// the diagnostics ring buffer. The observer reads ctx.Err() inside
// Send so the assertion runs before the handler's deferred cancel
// fires on the detached context.
func TestHandleEmailTest_DetachesRequestContext(t *testing.T) {
	t.Parallel()

	var observedErr error
	var observedHasDeadline bool
	recorder := &stubRecorder{
		observeCtx: func(ctx context.Context) {
			observedErr = ctx.Err()
			_, observedHasDeadline = ctx.Deadline()
		},
	}
	limiter := NewEmailRateLimiterWithClock(
		10*time.Second,
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
		nil,
	)

	form := "to=ops@example.test"
	ctx, cancel := context.WithCancel(t.Context())
	ctx = auth.WithPlayer(ctx, &auth.Player{ID: 1, Username: "admin", Email: "admin@example.test"})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/email/test", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "1.2.3.4:5555"
	rr := httptest.NewRecorder()

	// Cancel before serving so the handler sees an already-cancelled
	// request context; the send should still observe a fresh detached
	// context whose ctx.Err() is nil.
	cancel()
	HandleEmailTest(
		slog.New(slog.DiscardHandler),
		recorder, limiter, NewEmailFlash(testFlashKey, false),
	).ServeHTTP(rr, req)

	if got, want := recorder.callCnt, 1; got != want {
		t.Fatalf("recorder.callCnt = %d, want %d (cancellation must not skip the dispatch)", got, want)
	}
	if observedErr != nil {
		t.Errorf("observed ctx.Err = %v, want nil (detached from r.Context cancel)", observedErr)
	}
	if !observedHasDeadline {
		t.Error("observed ctx has no deadline; bounded timeout must apply")
	}
}

// TestHandleEmailTest_FlashEchoesTypedRecipient pins the input-echo
// behaviour: a typed "to" survives the PRG bounce so the admin sees
// their entry preserved instead of overwritten by the signed-in
// account's email.
func TestHandleEmailTest_FlashEchoesTypedRecipient(t *testing.T) {
	t.Parallel()

	recorder := &stubRecorder{sendErr: mailer.ErrNotConfigured}
	limiter := NewEmailRateLimiterWithClock(
		10*time.Second,
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
		nil,
	)

	rr := postEmailTest(t, recorder, limiter, "ops@example.test")

	fr := decodeFullFlashFromRecorder(t, rr)
	if got, want := fr.EchoTo, "ops@example.test"; got != want {
		t.Errorf("flash EchoTo = %q, want %q", got, want)
	}
}

func TestHandleEmailTestRefresh_RedirectsToAdminEmail(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/email/test", nil)
	rr := httptest.NewRecorder()

	HandleEmailTestRefresh().ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Location"), "/admin/email"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// testFlashKey is a deterministic signing key used by every flash
// helper the unit tests construct. The real value is irrelevant: the
// tests only verify that the cookie they read was signed with the
// same key the handler signed with.
var testFlashKey = []byte("test-flash-key-32-bytes-test-key")

// renderGET drives HandleEmailGet against an in-memory recorder and
// returns the response body. Folded out so the per-assertion tests do
// not repeat the boilerplate of constructing a logger / csrf manager /
// player context.
func renderGET(t *testing.T, status mailer.StatusView, recorder *stubRecorder) string {
	t.Helper()

	ctx := auth.WithPlayer(t.Context(), &auth.Player{ID: 1, Username: "admin", Email: "admin@example.test"})
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/email", nil)
	rr := httptest.NewRecorder()

	HandleEmailGet(
		slog.New(slog.DiscardHandler),
		csrf.New([]byte("test-key-32-bytes-test-key-32byt"), false),
		recorder, status, NewEmailFlash(testFlashKey, false),
	).ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d, body = %q", got, want, rr.Body.String())
	}

	return rr.Body.String()
}

// postEmailTest drives HandleEmailTest against an in-memory recorder
// and returns the recorder response. Folded out so each per-assertion
// test reads as a single setup + one assertion block. The synthetic
// RemoteAddr is fixed - every caller buckets on the same IP because
// the per-test limiter is fresh, so the IP itself does not matter.
func postEmailTest(
	t *testing.T,
	recorder *stubRecorder,
	limiter *EmailRateLimiter,
	recipient string,
) *httptest.ResponseRecorder {
	t.Helper()

	form := "to=" + recipient
	ctx := auth.WithPlayer(t.Context(), &auth.Player{ID: 1, Username: "admin", Email: "admin@example.test"})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/email/test", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "1.2.3.4:5555"
	rr := httptest.NewRecorder()

	HandleEmailTest(
		slog.New(slog.DiscardHandler),
		recorder, limiter, NewEmailFlash(testFlashKey, false),
	).ServeHTTP(rr, req)

	return rr
}

// assertRedirectToAdminEmail pins the PRG response shape every
// HandleEmailTest branch produces: 303 + Location: /admin/email.
// Inline so each test reads "post, assert redirect, decode flash".
func assertRedirectToAdminEmail(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Location"), "/admin/email"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// decodeFlashFromRecorder pulls the Set-Cookie the handler wrote out
// of the recorder, replays it into a GET /admin/email request, and
// returns the flash payload the helper observed. Failing to round-trip
// the cookie is a test bug, not a behaviour bug, so the helper fatals.
func decodeFlashFromRecorder(t *testing.T, rr *httptest.ResponseRecorder) (FlashKind, string, int) {
	t.Helper()

	fr := decodeFullFlashFromRecorder(t, rr)

	return fr.Kind, fr.Msg, fr.Wait
}

// decodeFullFlashFromRecorder is the [FlashRead]-typed variant used by
// the echo-recipient tests; the slim three-value form above stays for
// the existing assertions that do not care about EchoTo.
func decodeFullFlashFromRecorder(t *testing.T, rr *httptest.ResponseRecorder) FlashRead {
	t.Helper()

	flash := NewEmailFlash(testFlashKey, false)
	resp := rr.Result()
	cookies := resp.Cookies()
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("resp.Body.Close err = %v, want nil", err)
	}
	var flashCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "topbanana_admin_email_flash" {
			flashCookie = c

			break
		}
	}
	if flashCookie == nil {
		t.Fatal("expected a flash cookie in Set-Cookie; found none")
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/email", nil)
	req.AddCookie(flashCookie)
	fr := flash.Read(httptest.NewRecorder(), req)
	if !fr.OK {
		t.Fatalf("flash.Read did not return a value for cookie %q", flashCookie.Value)
	}

	return fr
}
