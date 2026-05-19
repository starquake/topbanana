package leaderboard_test

import (
	"sync"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/leaderboard"
)

func TestHub_PublishWithoutSubscribers(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	// Publish must not panic or deadlock when no one is listening.
	h.Publish(1)
}

func TestHub_DeliversToSubscriber(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	ch, unsub := h.Subscribe(7)
	defer unsub()

	h.Publish(7)

	select {
	case <-ch:
		// expected
	case <-time.After(time.Second):
		t.Fatal("Hub.Publish(7): subscriber received no event within 1s, want one event")
	}
}

func TestHub_ScopedByQuizID(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	chA, unsubA := h.Subscribe(1)
	defer unsubA()
	chB, unsubB := h.Subscribe(2)
	defer unsubB()

	h.Publish(1)

	select {
	case <-chA:
		// expected: quiz 1 subscriber sees the event
	case <-time.After(time.Second):
		t.Fatal("Hub.Publish(1): quiz-1 subscriber received no event within 1s, want one event")
	}

	select {
	case <-chB:
		t.Fatal("Hub.Publish(1): quiz-2 subscriber received an event, want no delivery (scoped by quizID)")
	case <-time.After(50 * time.Millisecond):
		// expected: quiz-2 buffer stays empty
	}
}

func TestHub_CoalescesBackPressure(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	ch, unsub := h.Subscribe(3)
	defer unsub()

	// Three publishes in a row; subscriber never drained between them.
	// Buffer is 1 so two of three events get dropped, but the read still
	// fires exactly once.
	h.Publish(3)
	h.Publish(3)
	h.Publish(3)

	if got, want := len(ch), 1; got != want {
		t.Errorf("subscriber buffer len = %d, want %d (Publish should coalesce, not block)", got, want)
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	ch, unsub := h.Subscribe(5)
	unsub()

	// After unsubscribe, the channel is closed; Publish must not panic on a
	// dropped subscriber.
	h.Publish(5)

	select {
	case _, ok := <-ch:
		if got, want := ok, false; got != want {
			t.Errorf("Hub.Subscribe: post-unsubscribe receive ok = %v, want %v (channel should be closed)", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Hub.Subscribe: closed channel not readable within 1s after unsubscribe")
	}
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	h := leaderboard.NewHub()
	_, unsub := h.Subscribe(9)

	unsub()
	// Second call must be a no-op rather than panicking on close-of-closed-channel.
	unsub()
}

func TestHub_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	// Sanity check that the mutex pattern doesn't deadlock under contention.
	// Not a strict race test (use `go test -race` for that); just a smoke
	// for "no goroutine blocks indefinitely."
	h := leaderboard.NewHub()
	const quizID int64 = 11
	const workers = 8

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			ch, unsub := h.Subscribe(quizID)
			defer unsub()
			h.Publish(quizID)
			select {
			case <-ch:
			case <-time.After(time.Second):
				t.Error("Hub.Publish: concurrent subscriber received no event within 1s, want one event")
			}
		})
	}
	wg.Wait()
}
