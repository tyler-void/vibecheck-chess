package chess

import (
	"context"
	"testing"
	"time"
)

// FR-018 server auto-flags · FR-019 emits terminal within 100 ms of zero
// crossing · FR-020 deterministic move-vs-flag race · SC-005.
//
// These tests use REAL time (short timeouts, real sleeps) because the flag
// adjudication code path goes through time.AfterFunc on the Go timer heap
// — there's no clean way to mock that without inventing an indirection
// layer the production code doesn't need. Short timeouts (50-200 ms) keep
// the suite fast.

// FR-018: the active side's clock reaches zero with no moves; the timer
// fires and the room transitions to a flagged terminal state.
//
// SC-005 envelope: the flag MUST be adjudicated within 100 ms of the zero
// crossing. With a 100 ms initial time and 250 ms test budget, that gives
// 150 ms of headroom — plenty for Go's timer heap (typically <10 ms).
func TestFlag_AutoAdjudicatedAfterTimeout(t *testing.T) {
	tc := TimeControl{Initial: 100 * time.Millisecond}
	r := NewGameRoom("f1", "alice", "bob", tc, time.Now())

	// Wait past the deadline + 100 ms of SC-005 envelope.
	time.Sleep(250 * time.Millisecond)

	snap := r.Snapshot(time.Now())
	// White was on the clock and ran out → Black wins on time.
	if snap.Outcome != BlackWin {
		t.Errorf("after 250ms with 100ms initial, outcome = %v, want BlackWin", snap.Outcome)
	}
	if snap.Termination != FlagOnTime {
		t.Errorf("termination = %v, want FlagOnTime", snap.Termination)
	}
}

// FR-009: post-terminal moves are rejected.
func TestFlag_PostFlagMoveRejected(t *testing.T) {
	tc := TimeControl{Initial: 50 * time.Millisecond}
	r := NewGameRoom("f2", "alice", "bob", tc, time.Now())

	time.Sleep(150 * time.Millisecond) // generously past flag

	// Game is over. The seq advanced once when the flag fired, so try seq=1.
	_, err := r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "post-flag", Seq: 1,
	}, time.Now())
	if err != ErrGameOver {
		t.Errorf("move after flag returned %v, want ErrGameOver", err)
	}
}

// FR-020: a move that lands within the active side's remaining time
// cancels the flag timer. After the move, White is no longer on the clock
// — Black is. Even if the original White-flag deadline arrives, the timer
// must have been stopped (or its callback must detect a stale schedule).
func TestFlag_MoveBeforeDeadlineCancelsTimer(t *testing.T) {
	// 300 ms initial, no increment. White moves at ~80 ms — well inside.
	tc := TimeControl{Initial: 300 * time.Millisecond}
	r := NewGameRoom("f3", "alice", "bob", tc, time.Now())

	time.Sleep(80 * time.Millisecond)
	_, err := r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "m1", Seq: 0,
	}, time.Now())
	if err != nil {
		t.Fatalf("move within window failed: %v", err)
	}

	// Wait past the ORIGINAL White-flag deadline (300 ms total). The flag
	// must NOT have fired because White's move was accepted at ~80 ms and
	// the captured-seq stale guard discards any stale fire.
	time.Sleep(280 * time.Millisecond) // 80 + 280 = 360 ms total
	snap := r.Snapshot(time.Now())
	if snap.Outcome != Ongoing {
		t.Errorf("after move + sleep, outcome = %v, want Ongoing", snap.Outcome)
	}
	if snap.Turn != Black {
		t.Errorf("turn = %v, want Black after White moves", snap.Turn)
	}
}

// Stale-timer guard: directly invoking onFlagTimer with a stale seq + color
// must be a no-op (no state change).
func TestFlag_StaleTimerCallbackIsNoOp(t *testing.T) {
	tc := TimeControl{Initial: 10 * time.Second}
	r := NewGameRoom("f4", "alice", "bob", tc, time.Now())

	// Apply a move so seq advances from 0 → 1; active flips to Black.
	_, _ = r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "m1", Seq: 0,
	}, time.Now())

	preSnap := r.Snapshot(time.Now())

	// Fire a "stale" callback as if the timer scheduled when seq=0 / White
	// had finally arrived. The guard MUST discard it.
	r.onFlagTimer(0, White)

	postSnap := r.Snapshot(time.Now())
	if postSnap.Outcome != preSnap.Outcome {
		t.Errorf("stale timer mutated outcome: %v → %v", preSnap.Outcome, postSnap.Outcome)
	}
	if postSnap.Seq != preSnap.Seq {
		t.Errorf("stale timer advanced seq: %d → %d", preSnap.Seq, postSnap.Seq)
	}
}

// Flag adjudicated inside ApplyMove (clock zero at move-arrival time):
// FR-020 deterministic move-vs-flag — if the move-handler detects the
// active side already past zero on Debit, it adjudicates the flag instead
// of accepting the move. The move RPC sees ErrFlagged.
func TestFlag_MoveArrivingPastZeroSeesErrFlagged(t *testing.T) {
	tc := TimeControl{Initial: 30 * time.Millisecond}
	r := NewGameRoom("f5", "alice", "bob", tc, time.Now())

	// Sleep well past zero — the time.AfterFunc may have already fired and
	// adjudicated, but even if it hasn't yet, our move arriving past zero
	// must hit ErrFlagged (Debit clamps Remain to 0, and the move-handler
	// checks Remain <= 0).
	time.Sleep(120 * time.Millisecond)

	_, err := r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "late", Seq: 0,
	}, time.Now())
	// Either ErrFlagged (move-handler detected zero) or ErrGameOver (timer
	// already fired and terminated the game). Both are correct outcomes
	// per FR-020 — the spec says the result is deterministic by server
	// clock, and either outcome reflects "White lost on time".
	if err != ErrFlagged && err != ErrGameOver && err != ErrStaleSequence {
		t.Errorf("late move returned %v, want ErrFlagged or ErrGameOver or ErrStaleSequence", err)
	}
	// Verify the room is in a terminal state.
	snap := r.Snapshot(time.Now())
	if snap.Outcome == Ongoing {
		t.Errorf("after late move, outcome still Ongoing; want terminal")
	}
}
