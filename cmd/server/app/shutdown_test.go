package app_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	. "github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/dbtest"
)

// TestRunHTTPServer_DrainsEmailTasksBeforeReturning pins the #740 / #741 fix:
// a detached email-dispatch goroutine that is still doing DB work when
// shutdown begins is drained before RunHTTPServer returns. Production closes
// the DB right after RunHTTPServer returns (Run's deferred conn.Close), so a
// dispatch that outlived the return would write to a closed connection - the
// data race the two issues reported. The test holds a tracked task in-flight
// past the shutdown signal, asserts the serve loop does not return while the
// task is blocked, then releases it: the task's real DB query must succeed
// (it ran while the connection was still open) and a query issued after the
// caller closes the DB must fail, proving the connection the drain protected
// is the same one the close tears down.
//
// The root ctx is cancelled at shutdown alongside signalCtx, mirroring
// production (signal-driven) and the integration harness (which cancels the
// same ctx it passes to Run). That pins the drain budget being detached from
// ctx: a plain WithTimeout(ctx, ...) would fire instantly here and skip the
// wait.
func TestRunHTTPServer_DrainsEmailTasksBeforeReturning(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)

	ctx, cancelRoot := context.WithCancel(t.Context())
	defer cancelRoot()
	signalCtx, stopSignal := context.WithCancel(ctx)

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen err = %v, want nil", err)
	}

	tasks := bgtasks.New()
	started := make(chan struct{})
	release := make(chan struct{})
	var dbErr error
	tasks.Go(func() {
		close(started)
		<-release
		// A real query against the live connection, mirroring the token /
		// player I/O the production dispatches perform. The context is
		// detached from the cancelled root the way the production dispatch
		// detaches via WithoutCancel, so the ping fails only if the DB is
		// closed, not because ctx was cancelled.
		dbErr = db.PingContext(context.WithoutCancel(ctx))
	})

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- RunHTTPServer(
			ctx, signalCtx, ln,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
			tasks,
			slog.New(slog.DiscardHandler),
		)
	}()

	<-started
	stopSignal()
	cancelRoot()

	// The serve loop must not return while the tracked task is still in
	// flight: it is blocked on release, so a return now would mean shutdown
	// skipped the drain (the bug).
	select {
	case err := <-serveDone:
		t.Fatalf("RunHTTPServer returned before the in-flight task finished: err = %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("RunHTTPServer err = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTPServer did not return after the task was released")
	}

	if dbErr != nil {
		t.Fatalf("tracked task DB query err = %v, want nil (drain must run before the DB closes)", dbErr)
	}

	// Closing the DB only after the serve loop returned mirrors Run's
	// deferred conn.Close. A query now must fail, confirming the drain
	// protected the same connection the close tears down.
	if cerr := db.Close(); cerr != nil {
		t.Fatalf("db.Close err = %v, want nil", cerr)
	}
	if got := db.PingContext(context.WithoutCancel(ctx)); got == nil {
		t.Error("PingContext after Close = nil, want an error (sanity: close really tears the connection down)")
	}
}
