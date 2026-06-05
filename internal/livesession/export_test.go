package livesession

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
