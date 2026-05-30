package sse

import (
	"sync"
	"testing"
	"time"
)

// FR-027 / FR-028 / SC-009: a single slow subscriber MUST NOT delay any
// healthy subscriber. The hub's non-blocking publish + drop-on-overflow
// policy guarantees this. These tests are explicitly named for US3
// (spectator broadcast) — the prod path is identical to TestHub_*, but
// here we frame it as the production scenario: many spectators, one
// stalls, the rest are unaffected.

// 50 healthy spectators + 1 deliberately stalled. After many publishes,
// the stalled spectator is dropped (DroppedCount = 1) and every healthy
// spectator received every message.
func TestSlowReader_OneStallDoesNotAffectFifty(t *testing.T) {
	h := NewFanoutHub()

	const (
		healthyCount = 50
		numPublish   = sendBufferSize + 20 // enough to fill stalled queue + tail
	)

	// Set up: healthyCount drainer goroutines, each reads as fast as it can.
	healthy := make([]*SSEClient, healthyCount)
	var wg sync.WaitGroup
	received := make([]int, healthyCount)
	for i := 0; i < healthyCount; i++ {
		healthy[i] = h.Register()
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range healthy[i].Send {
				received[i]++
			}
		}()
	}

	// 1 stalled subscriber — never reads. Its Send queue will fill on the
	// (sendBufferSize+1)th publish and trigger the drop.
	stalled := h.Register()

	// Publisher loop. Use a tiny per-publish yield so the drainer goroutines
	// get scheduler time — otherwise on a tight loop the publisher can
	// outpace receivers under the race detector and falsely drop a healthy
	// subscriber too.
	for i := 0; i < numPublish; i++ {
		h.Publish([]byte{byte(i)})
		time.Sleep(50 * time.Microsecond) // yield to drainers
	}

	// Give drainers time to finish processing buffered messages.
	time.Sleep(20 * time.Millisecond)

	// Tear down healthy subscribers so their goroutines exit cleanly.
	for _, c := range healthy {
		h.Unregister(c.ID)
	}
	wg.Wait()

	// Assertions.
	if got := h.DroppedCount(); got != 1 {
		t.Errorf("DroppedCount = %d, want exactly 1 (the stalled subscriber)", got)
	}

	for i, n := range received {
		if n != numPublish {
			t.Errorf("healthy subscriber %d received %d messages, want %d", i, n, numPublish)
		}
	}

	// Drain the stalled channel (it's closed by the hub).
	for range stalled.Send {
	}
}

// Hub.Publish must drop ALL slow subscribers on a single publish, not just
// one at a time. Set up N stalled subscribers; the first publish that
// exceeds sendBufferSize must drop them all together.
func TestSlowReader_MultipleStalledDroppedSimultaneously(t *testing.T) {
	h := NewFanoutHub()

	const stalledCount = 10
	stalled := make([]*SSEClient, stalledCount)
	for i := 0; i < stalledCount; i++ {
		stalled[i] = h.Register()
	}

	// Fill every queue exactly to capacity. None drop yet.
	for i := 0; i < sendBufferSize; i++ {
		h.Publish([]byte{byte(i)})
	}
	if h.DroppedCount() != 0 {
		t.Fatalf("after %d publishes (= buffer), DroppedCount = %d, want 0",
			sendBufferSize, h.DroppedCount())
	}
	if h.Count() != stalledCount {
		t.Fatalf("after filling queues, Count = %d, want %d", h.Count(), stalledCount)
	}

	// One more publish — every queue is full → every subscriber dropped.
	h.Publish([]byte{99})
	if got := h.DroppedCount(); got != stalledCount {
		t.Errorf("DroppedCount = %d, want %d (all stalled subscribers dropped together)",
			got, stalledCount)
	}
	if got := h.Count(); got != 0 {
		t.Errorf("Count = %d, want 0", got)
	}

	// All channels should now be closed.
	for i, c := range stalled {
		select {
		case _, ok := <-c.Send:
			_ = ok
			// First read may yield a buffered message; that's fine. Just
			// confirm we can read the channel exhaustively.
			for range c.Send {
			}
		default:
			t.Errorf("stalled[%d] Send not drainable; channel may not be closed", i)
		}
	}
}
