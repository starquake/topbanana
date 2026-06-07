// Package bgtasks tracks detached background goroutines so a graceful
// shutdown can wait for them to finish before the process tears down shared
// resources.
//
// Several auth/profile/admin handlers dispatch their email send on a detached
// goroutine so the HTTP response time stays independent of SMTP latency (and
// account-existence-opaque). Those goroutines mint tokens and read players
// through the DB store. Untracked, one can still be writing to the database
// when the connection is closed on shutdown - a use-after-close data race
// (#740, #741). A [Tracker] registers each such goroutine so shutdown can
// drain them before the DB closes, while a bounded wait keeps a stuck SMTP
// server from hanging shutdown forever.
package bgtasks

import (
	"context"
	"fmt"
	"sync"
)

// Tracker tracks detached background goroutines and lets a caller wait for
// them to finish. The zero value is ready to use; [New] is provided for
// symmetry. A nil *Tracker is valid and runs goroutines untracked, so handlers
// wired without a tracker (e.g. unit tests) keep their detached-dispatch
// behaviour without a per-call nil guard.
type Tracker struct {
	wg sync.WaitGroup
}

// New returns a ready Tracker.
func New() *Tracker { return &Tracker{} }

// Go runs fn on a tracked goroutine. Every fn started this way is awaited by
// [Tracker.Wait], so a started fn always finishes before Wait returns. On a
// nil receiver fn runs on a plain untracked goroutine.
func (t *Tracker) Go(fn func()) {
	if t == nil {
		go fn()

		return
	}
	t.wg.Go(fn)
}

// Wait blocks until every goroutine started with [Tracker.Go] has returned or
// ctx is done, whichever comes first. It returns nil when the goroutines
// drained and ctx.Err() when ctx fired first, so a caller can bound shutdown
// against a stuck task. On a nil receiver it returns nil immediately.
func (t *Tracker) Wait(ctx context.Context) error {
	if t == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("bgtasks: wait: %w", ctx.Err())
	}
}
