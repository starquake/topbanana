package bgtasks_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/bgtasks"
)

// TestTracker_WaitBlocksUntilTasksFinish pins that Wait does not return until
// every goroutine started with Go has finished - the property the shutdown
// drain relies on to keep a dispatch from outliving the DB (#740).
func TestTracker_WaitBlocksUntilTasksFinish(t *testing.T) {
	t.Parallel()

	tr := New()
	var finished atomic.Bool
	release := make(chan struct{})
	tr.Go(func() {
		<-release
		finished.Store(true)
	})

	waitDone := make(chan error, 1)
	go func() { waitDone <- tr.Wait(t.Context()) }()

	select {
	case <-waitDone:
		t.Fatal("Wait returned before the task finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("Wait err = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after the task finished")
	}
	if got, want := finished.Load(), true; got != want {
		t.Errorf("finished = %v, want %v", got, want)
	}
}

// TestTracker_WaitHonoursContextDeadline pins that a stuck task cannot hang
// the shutdown forever: Wait gives up with the context error when the bound
// fires before the task drains.
func TestTracker_WaitHonoursContextDeadline(t *testing.T) {
	t.Parallel()

	tr := New()
	release := make(chan struct{})
	defer close(release)
	tr.Go(func() { <-release })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if got, want := tr.Wait(ctx), context.DeadlineExceeded; !errors.Is(got, want) {
		t.Errorf("Wait err = %v, want %v", got, want)
	}
}

// TestTracker_NilRunsUntracked pins the nil-receiver contract the handler
// wiring leans on: a handler built without a tracker (unit tests) still
// dispatches, and Wait on a nil tracker returns immediately.
func TestTracker_NilRunsUntracked(t *testing.T) {
	t.Parallel()

	var tr *Tracker
	ran := make(chan struct{})
	tr.Go(func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("nil-tracker Go did not run the function")
	}

	if got := tr.Wait(t.Context()); got != nil {
		t.Errorf("nil-tracker Wait err = %v, want nil", got)
	}
}
