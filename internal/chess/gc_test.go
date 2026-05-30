package chess

import (
	"context"
	"testing"
	"time"
)

// FR-031: GameManager's GC sweep evicts terminal rooms with no subscribers
// after EvictAfter. This test directly invokes evictTerminalIdle with a
// synthetic `now` so it runs in microseconds, no real sleeps.

func TestGC_EvictsTerminalIdleRoomsAfterGracePeriod(t *testing.T) {
	// Shorten EvictAfter for the test; restore afterwards.
	orig := EvictAfter
	EvictAfter = 100 * time.Millisecond
	defer func() { EvictAfter = orig }()

	mgr := NewGameManager()
	now := time.Now()

	// Three rooms: one terminal+idle past grace, one terminal but still in
	// grace, one still ongoing.
	terminalIdle, _ := mgr.Create("evict-me", "a", "b",
		TimeControl{Initial: 1 * time.Minute}, now)
	terminalRecent, _ := mgr.Create("too-soon", "c", "d",
		TimeControl{Initial: 1 * time.Minute}, now)
	ongoing, _ := mgr.Create("still-alive", "e", "f",
		TimeControl{Initial: 1 * time.Minute}, now)

	// Force evict-me into a terminal-past-grace state.
	terminalIdle.mu.Lock()
	terminalIdle.outcome = WhiteWin
	terminalIdle.termination = Resignation
	terminalIdle.clocks.LastTick = now.Add(-time.Second) // 1s ago, past 100ms grace
	terminalIdle.mu.Unlock()

	// Force too-soon into terminal but still in grace.
	terminalRecent.mu.Lock()
	terminalRecent.outcome = WhiteWin
	terminalRecent.termination = Resignation
	terminalRecent.clocks.LastTick = now // 0 ago, well inside grace
	terminalRecent.mu.Unlock()

	_ = ongoing // remains Ongoing — must not be evicted

	// Run a sweep.
	evicted := mgr.evictTerminalIdle(now)

	if len(evicted) != 1 || evicted[0] != "evict-me" {
		t.Errorf("evicted = %v, want [evict-me]", evicted)
	}
	if _, ok := mgr.Get("evict-me"); ok {
		t.Errorf("evict-me still in manager after sweep")
	}
	if _, ok := mgr.Get("too-soon"); !ok {
		t.Errorf("too-soon was evicted prematurely (still in grace)")
	}
	if _, ok := mgr.Get("still-alive"); !ok {
		t.Errorf("still-alive was evicted (was Ongoing)")
	}
}

// FR-031: a terminal room with subscribers is NOT evictable. Late-arriving
// spectators on a finished game still need their stream.
func TestGC_DoesNotEvictTerminalRoomWithSubscribers(t *testing.T) {
	orig := EvictAfter
	EvictAfter = 1 * time.Millisecond
	defer func() { EvictAfter = orig }()

	mgr := NewGameManager()
	now := time.Now()

	room, _ := mgr.Create("g", "a", "b",
		TimeControl{Initial: 1 * time.Minute}, now)

	room.mu.Lock()
	room.outcome = Draw
	room.termination = AgreedDraw
	room.clocks.LastTick = now.Add(-time.Second) // well past 1ms grace
	room.mu.Unlock()

	// Register a subscriber on the hub so Count() > 0.
	c := room.hub.Register()
	defer room.hub.Unregister(c.ID)

	evicted := mgr.evictTerminalIdle(now)
	if len(evicted) != 0 {
		t.Errorf("evicted = %v, want empty (subscriber present)", evicted)
	}
	if _, ok := mgr.Get("g"); !ok {
		t.Fatalf("room evicted despite active subscriber")
	}
}

// StartGC launches the loop and exits on ctx cancellation. Quick liveness
// test (no real sleep — cancel immediately).
func TestGC_StartGCExitsOnCancel(t *testing.T) {
	mgr := NewGameManager()
	ctx, cancel := context.WithCancel(context.Background())
	mgr.StartGC(ctx)

	// Cancel immediately. The goroutine should exit within a tick of the
	// gcInterval ticker (1 min in prod). We can't wait for that in CI, so
	// we just confirm the cancel doesn't deadlock anything observable.
	cancel()
	// Tiny sleep to let the goroutine notice ctx.Done.
	time.Sleep(5 * time.Millisecond)
}
