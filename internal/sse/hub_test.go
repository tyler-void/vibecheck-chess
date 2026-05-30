package sse

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kisschess/internal/chess"
)

// FR-022 broadcast only to game's subscribers · FR-027 no slow blocking ·
// FR-028 drop slow readers · Constitution III lock-ordering · Constitution IV
// bounded non-blocking fan-out.

// Two healthy subscribers receive every event in order.
func TestHub_PublishDeliversToAll(t *testing.T) {
	h := NewFanoutHub()
	a := h.Register()
	b := h.Register()

	for i := 0; i < 5; i++ {
		h.Publish([]byte{byte(i)})
	}

	// Drain.
	got := func(c *SSEClient) []byte {
		var out []byte
		for i := 0; i < 5; i++ {
			select {
			case msg := <-c.Send:
				out = append(out, msg[0])
			case <-time.After(time.Second):
				t.Fatalf("client %d timed out reading message %d", c.ID, i)
			}
		}
		return out
	}
	gotA := got(a)
	gotB := got(b)
	want := []byte{0, 1, 2, 3, 4}
	if string(gotA) != string(want) || string(gotB) != string(want) {
		t.Errorf("delivery mismatch: a=%v b=%v want=%v", gotA, gotB, want)
	}
	h.Unregister(a.ID)
	h.Unregister(b.ID)
}

// FR-027 / FR-028: a slow reader (queue full) is dropped on the next publish
// and does not slow other subscribers. The drop counter increments.
//
// Synchronised drainer: we drain fast inline with each publish so its queue
// can never fill regardless of scheduler timing. This isolates the slow-reader
// behaviour to slow alone.
func TestHub_SlowReaderDropped(t *testing.T) {
	h := NewFanoutHub()
	fast := h.Register()
	slow := h.Register()

	// Send more than sendBufferSize so slow's queue overflows and slow gets
	// dropped. We keep fast in lockstep by draining each message immediately.
	const N = sendBufferSize + 5
	for i := 0; i < N; i++ {
		h.Publish([]byte{byte(i)})
		select {
		case got := <-fast.Send:
			if got[0] != byte(i) {
				t.Fatalf("fast got %d at i=%d", got[0], i)
			}
		case <-time.After(time.Second):
			t.Fatalf("fast didn't receive message %d", i)
		}
	}

	if got := h.DroppedCount(); got != 1 {
		t.Errorf("DroppedCount after burst = %d, want 1 (slow only)", got)
	}
	if got := h.Count(); got != 1 {
		t.Errorf("Count after burst = %d, want 1 (fast still attached)", got)
	}

	// slow's Send must be closed by the hub.
	select {
	case _, ok := <-slow.Send:
		// First receive may yield a buffered message; that's fine.
		_ = ok
	default:
	}
	// Final unregister of fast, drain whatever closes.
	h.Unregister(fast.ID)
	for range fast.Send {
	}
	for range slow.Send {
	}
}

// Concurrent register/unregister/publish under -race: no data races, every
// remaining subscriber observes a coherent (possibly truncated by drops)
// stream.
func TestHub_ConcurrentRegisterUnregisterPublish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent stress in -short mode")
	}
	h := NewFanoutHub()

	const (
		numSubscribers = 200
		numPublishes   = 500
	)

	var wg sync.WaitGroup
	var connected atomic.Int64

	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.Register()
			connected.Add(1)
			defer func() {
				h.Unregister(c.ID)
				connected.Add(-1)
			}()
			// Drain whatever arrives until the channel is closed.
			for range c.Send {
			}
		}()
	}

	// Wait for at least half to connect before publishing.
	for connected.Load() < numSubscribers/2 {
		time.Sleep(time.Millisecond)
	}

	var pubWG sync.WaitGroup
	for i := 0; i < 4; i++ {
		pubWG.Add(1)
		go func() {
			defer pubWG.Done()
			for j := 0; j < numPublishes; j++ {
				h.Publish([]byte{byte(j)})
			}
		}()
	}
	pubWG.Wait()

	// Drain by tearing down all subscribers.
	// Subscribers exit their goroutine when their Send channel closes; that
	// happens via Unregister inside each goroutine's defer. Since they're
	// blocked on `range c.Send` we have to close from the outside.
	// Easiest: just unregister every remaining id we can find.
	for h.Count() > 0 {
		// Read a snapshot of ids under the lock.
		ids := func() []uint64 {
			h.mu.RLock()
			defer h.mu.RUnlock()
			out := make([]uint64, 0, len(h.clients))
			for id := range h.clients {
				out = append(out, uint64(id))
			}
			return out
		}()
		for _, id := range ids {
			h.Unregister(chess.ClientID(id))
		}
	}
	wg.Wait()
}
