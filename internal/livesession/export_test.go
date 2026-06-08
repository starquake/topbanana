package livesession

import (
	"context"
	"time"
)

// ExportNewServiceWithCodeGen re-exports newServiceWithCodeGen so the
// external test package can construct a Service with an injected join-code
// generator (to force collisions deterministically) without widening the
// production API.
var ExportNewServiceWithCodeGen = newServiceWithCodeGen

// ExportHubSubscriberCount reports how many live subscribers the hub holds
// for the given code, so a test can assert that unsubscribe (the SSE
// handler's disconnect cleanup) actually drops the entry rather than
// leaking it. Test-only; not part of the production API.
func ExportHubSubscriberCount(h *Hub, code string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.subs[code])
}

// ExportHubHasVersion reports whether the hub still holds a version entry for
// the given code, so a test can assert that a terminal session is forgotten
// (the entry evicted) rather than pinned for the process lifetime. Test-only;
// not part of the production API.
func ExportHubHasVersion(h *Hub, code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	_, ok := h.versions[code]

	return ok
}

// ExportRunnerTick drives one runner scan at the given instant, so a test can
// advance a session through its phases off a controlled clock without waiting
// on the beat ticker. Test-only.
func ExportRunnerTick(ctx context.Context, r *Runner, now time.Time) {
	r.tick(ctx, now)
}
