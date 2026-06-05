package livesession_test

import (
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/livesession"
)

func TestHub_PublishWithoutSubscribers(t *testing.T) {
	t.Parallel()

	h := NewHub()
	// Publish must not panic or deadlock when no one is listening, and it
	// still bumps the version.
	if got, want := h.Publish("ROOM01", PhaseLobby).Version, uint64(1); got != want {
		t.Errorf("Publish version = %d, want %d", got, want)
	}
}

func TestHub_DeliversTickWithVersionAndPhase(t *testing.T) {
	t.Parallel()

	h := NewHub()
	ch, version, unsub := h.Subscribe("ROOM01")
	defer unsub()
	if got, want := version, uint64(0); got != want {
		t.Errorf("Subscribe version = %d, want %d (no publish yet)", got, want)
	}

	h.Publish("ROOM01", PhaseLobby)

	select {
	case tick := <-ch:
		if got, want := tick.Version, uint64(1); got != want {
			t.Errorf("tick.Version = %d, want %d", got, want)
		}
		if got, want := tick.Phase, PhaseLobby; got != want {
			t.Errorf("tick.Phase = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received no tick within 1s, want one")
	}
}

func TestHub_VersionIsMonotonicPerSession(t *testing.T) {
	t.Parallel()

	h := NewHub()
	if got, want := h.Publish("ROOM01", PhaseLobby).Version, uint64(1); got != want {
		t.Errorf("first publish version = %d, want %d", got, want)
	}
	if got, want := h.Publish("ROOM01", PhaseLobby).Version, uint64(2); got != want {
		t.Errorf("second publish version = %d, want %d", got, want)
	}
	// A fresh subscriber sees the latest version, not zero.
	_, version, unsub := h.Subscribe("ROOM01")
	defer unsub()
	if got, want := version, uint64(2); got != want {
		t.Errorf("late subscriber version = %d, want %d", got, want)
	}
}

func TestHub_ScopedByCode(t *testing.T) {
	t.Parallel()

	h := NewHub()
	chA, _, unsubA := h.Subscribe("ROOMAA")
	defer unsubA()
	chB, _, unsubB := h.Subscribe("ROOMBB")
	defer unsubB()

	h.Publish("ROOMAA", PhaseLobby)

	select {
	case <-chA:
		// expected
	case <-time.After(time.Second):
		t.Fatal("Publish(ROOMAA): ROOMAA subscriber received no tick within 1s, want one")
	}

	select {
	case <-chB:
		t.Fatal("Publish(ROOMAA): ROOMBB subscriber received a tick, want none (scoped by code)")
	case <-time.After(50 * time.Millisecond):
		// expected: the other session's buffer stays empty.
	}
}

func TestHub_CoalescesBackPressure(t *testing.T) {
	t.Parallel()

	h := NewHub()
	ch, _, unsub := h.Subscribe("ROOM01")
	defer unsub()

	// Three publishes with the subscriber never draining: buffer is 1 so
	// two get dropped, but exactly one tick is readable - carrying the
	// latest version.
	h.Publish("ROOM01", PhaseLobby)
	h.Publish("ROOM01", PhaseLobby)
	h.Publish("ROOM01", PhaseLobby)

	if got, want := len(ch), 1; got != want {
		t.Fatalf("subscriber buffer len = %d, want %d (Publish should coalesce, not block)", got, want)
	}
	tick := <-ch
	if got, want := tick.Version, uint64(1); got != want {
		t.Errorf("coalesced tick version = %d, want %d (first publish wins the buffer slot)", got, want)
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()

	h := NewHub()
	ch, _, unsub := h.Subscribe("ROOM01")
	unsub()

	// After unsubscribe the channel is closed; Publish must not panic on a
	// dropped subscriber.
	h.Publish("ROOM01", PhaseLobby)

	select {
	case _, ok := <-ch:
		if got, want := ok, false; got != want {
			t.Errorf("post-unsubscribe receive ok = %v, want %v (channel should be closed)", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("closed channel not readable within 1s after unsubscribe")
	}
}

func TestHub_UnsubscribeDropsSubscriber(t *testing.T) {
	t.Parallel()

	// The SSE handler unsubscribes on client disconnect; this pins that the
	// hub then holds no entry for the code (no leaked map entry / goroutine
	// pin on a long-lived session).
	h := NewHub()
	_, _, unsub := h.Subscribe("ROOM01")
	if got, want := ExportHubSubscriberCount(h, "ROOM01"), 1; got != want {
		t.Fatalf("subscriber count after Subscribe = %d, want %d", got, want)
	}

	unsub()
	if got, want := ExportHubSubscriberCount(h, "ROOM01"), 0; got != want {
		t.Errorf("subscriber count after unsubscribe = %d, want %d (hub must drop the subscriber)", got, want)
	}
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	h := NewHub()
	_, _, unsub := h.Subscribe("ROOM01")

	unsub()
	// Second call must be a no-op, not a panic on close-of-closed-channel.
	unsub()
}

func TestHub_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	// Smoke check that the mutex pattern does not deadlock under contention.
	h := NewHub()
	const code = "ROOM01"
	const workers = 8

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			ch, _, unsub := h.Subscribe(code)
			defer unsub()
			h.Publish(code, PhaseLobby)
			select {
			case <-ch:
			case <-time.After(time.Second):
				t.Error("concurrent subscriber received no tick within 1s, want one")
			}
		})
	}
	wg.Wait()
}
