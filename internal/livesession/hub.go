package livesession

import "sync"

// Tick is the minimal payload the session event channel fans out on every
// state change. It deliberately carries NO game data - no roster, quiz, or
// player fields. A tick means "session state moved; re-GET
// /api/sessions/{code}/state". Version is a monotonic per-session counter
// so a subscriber can tell ticks apart and detect coalesced gaps; Phase is
// the session phase at publish time so a surface can branch without an
// immediate state fetch (it still re-GETs for the authoritative read).
type Tick struct {
	Version uint64 `json:"version"`
	Phase   Phase  `json:"phase"`
}

// Hub fans out per-session ticks to in-process subscribers and owns the
// monotonic per-session version counter. Safe for concurrent use. Each
// subscriber gets one buffered slot so a slow reader never blocks Publish;
// a dropped tick is harmless because the subscriber re-GETs the full state
// on the next receive (the side-channel carries no data of its own).
//
// This mirrors leaderboard.Hub. It is keyed by join code (a string) rather
// than quiz id, and the published value is a Tick rather than a bare
// struct{} because the session channel reports a {version, phase} on each
// transition while the leaderboard channel only signals "refetch".
//
// The version counter lives here, in memory: MP-2 needs no DB version
// column. Versions persist for a code even while it has no subscribers, so
// a client that reconnects sees a version at least as high as the last one
// it saw.
type Hub struct {
	mu       sync.Mutex
	subs     map[string]map[chan Tick]struct{}
	versions map[string]uint64
}

// NewHub returns a fresh Hub with no subscribers and no versions.
func NewHub() *Hub {
	return &Hub{
		subs:     make(map[string]map[chan Tick]struct{}),
		versions: make(map[string]uint64),
	}
}

// Subscribe registers a receiver for the given session join code and
// returns a receive-only channel that fires a Tick on every Publish, plus
// the current version so the caller can emit an initial frame without
// racing a Publish. The caller MUST invoke the returned unsubscribe func
// when done (typically via defer) - failing to do so leaks a map entry and
// pins memory on long-lived sessions.
//
// The channel is buffered (capacity 1). If a Publish lands while the
// previous tick is still unread, the new one is dropped; the subscriber
// re-GETs the current state on every receive, so a dropped tick is a
// coalesced repaint, not lost data.
func (h *Hub) Subscribe(code string) (<-chan Tick, uint64, func()) {
	ch := make(chan Tick, 1)
	h.mu.Lock()
	set, ok := h.subs[code]
	if !ok {
		set = make(map[chan Tick]struct{})
		h.subs[code] = set
	}
	set[ch] = struct{}{}
	version := h.versions[code]
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if existing, ok := h.subs[code]; ok {
				delete(existing, ch)
				if len(existing) == 0 {
					delete(h.subs, code)
				}
			}
			// Close under the lock so a concurrent Publish (which writes
			// under the same lock) cannot race with the close.
			close(ch)
		})
	}

	return ch, version, unsubscribe
}

// Forget drops the session's version counter once it has reached a terminal
// state and no client can produce more ticks for it. Without this a code's
// versions entry would live for the whole process lifetime, since unsubscribe
// only clears subs. Safe for concurrent use: it runs under the hub mutex, the
// same lock Publish and Subscribe take.
//
// Eviction is safe at finish: a client that reconnects after Forget
// re-subscribes (recreating the entry) and Subscribe hands it version 0, which
// is sane for a finished session that emits no further ticks. The runner calls
// Forget only after the final Publish, so the finished tick still carries the
// last real version.
func (h *Hub) Forget(code string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.versions, code)
}

// Publish bumps the session's version counter, then fires a non-blocking
// tick carrying the new version and the given phase to every active
// subscriber of the code. Returns the published Tick. If a subscriber's
// buffer is full the tick is dropped on the floor; the subscriber sees the
// next tick and re-GETs.
//
// The whole operation runs under the hub mutex so close-channel (in
// unsubscribe) and chan-send never overlap, and the version increment is
// atomic with the fan-out. Sends are non-blocking (buffer = 1, select
// default), so the worst case is O(subscribers) - fine for the small
// per-session subscriber set we expect.
func (h *Hub) Publish(code string, phase Phase) Tick {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.versions[code]++
	tick := Tick{Version: h.versions[code], Phase: phase}

	for ch := range h.subs[code] {
		select {
		case ch <- tick:
		default:
			// buffer full; coalesced on the receive side.
		}
	}

	return tick
}
