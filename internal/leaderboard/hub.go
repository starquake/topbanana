// Package leaderboard provides a process-local pub/sub for quiz-leaderboard
// changes. game.Service.SubmitAnswer publishes a tick whenever an answer
// commits; SSE handlers subscribe and re-render the leaderboard on each
// tick. Coalescing is intentional — the channel buffer is 1, so a slow
// subscriber drops intermediate events and just sees "leaderboard moved"
// on the next receive. Subscribers should treat each event as a "fetch
// the latest" signal, not a per-answer delta.
package leaderboard

import "sync"

// Hub fans out per-quiz events to in-process subscribers. Safe for
// concurrent use. Bounded to one buffered slot per subscriber so a slow
// reader never blocks Publish.
type Hub struct {
	mu   sync.Mutex
	subs map[int64]map[chan struct{}]struct{}
}

// NewHub returns a fresh Hub with no subscribers.
func NewHub() *Hub {
	return &Hub{subs: make(map[int64]map[chan struct{}]struct{})}
}

// Subscribe registers a receiver for the given quiz and returns a
// receive-only channel that fires once on every Publish. The caller MUST
// invoke the returned unsubscribe func when done (typically via defer)
// — failing to do so leaks a map entry and pins memory on long-lived
// quizzes.
//
// The channel is buffered (capacity 1). If a Publish lands while the
// previous event is still unread, the new one is dropped. Subscribers
// re-fetch the current state on every receive, so dropped events are
// not lost data — just a coalesced repaint.
func (h *Hub) Subscribe(quizID int64) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	set, ok := h.subs[quizID]
	if !ok {
		set = make(map[chan struct{}]struct{})
		h.subs[quizID] = set
	}
	set[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if existing, ok := h.subs[quizID]; ok {
				delete(existing, ch)
				if len(existing) == 0 {
					delete(h.subs, quizID)
				}
			}
			// Close under the lock so a concurrent Publish (which writes
			// under the same lock) cannot race with the close.
			close(ch)
		})
	}

	return ch, unsubscribe
}

// Publish fires a non-blocking tick to every active subscriber of the
// given quiz. If a subscriber's buffer is full, the event is dropped on
// the floor; the subscriber will see the next event and re-fetch.
//
// The whole operation runs under the hub mutex so close-channel (in
// unsubscribe) and chan-send never overlap. Sends are non-blocking
// (buffer = 1, select default), so the worst case is O(subscribers)
// CAS attempts — fine for the small per-quiz subscriber set we expect.
func (h *Hub) Publish(quizID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[quizID]
	if !ok {
		return
	}
	for ch := range set {
		select {
		case ch <- struct{}{}:
		default:
			// buffer full; coalesced on the receive side.
		}
	}
}
