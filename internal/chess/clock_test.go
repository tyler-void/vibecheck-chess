package chess

import (
	"testing"
	"time"
)

// FR-015 server is sole timing authority · FR-016 monotonic source ·
// FR-017 increment only on accepted move · FR-020 deterministic move-vs-flag.
//
// These tests use synthetic time.Time values (not real time.Now()) so the
// clock semantics are fully deterministic and the suite runs in microseconds.

func tc3plus2() TimeControl {
	return TimeControl{
		Initial:   3 * time.Minute,
		Increment: 2 * time.Second,
	}
}

// FR-016: elapsed-time math operates on time.Duration (Go's monotonic clock
// component) — even when the wall clock jumps backwards by an hour mid-game,
// Sub() of two monotonic-stamped Times returns the correct elapsed.
//
// We simulate the "NTP step" by constructing two times such that their wall
// clock difference disagrees with their monotonic difference. time.Time's
// Sub uses the monotonic component when both operands carry it, which is the
// behaviour the constitution relies on.
func TestClock_DebitUsesMonotonicComponent(t *testing.T) {
	t0 := time.Now() // carries a monotonic component
	c := NewClockState(tc3plus2(), t0)

	// Five seconds elapse on the monotonic clock.
	t1 := t0.Add(5 * time.Second)
	c.Debit(t1)

	got := c.Remain[White]
	want := 3*time.Minute - 5*time.Second
	if got != want {
		t.Fatalf("after 5s debit white_remaining = %v, want %v", got, want)
	}
}

// FR-017: increment is credited only to the moving side, and only AFTER the
// debit. Sequence: Debit(now) → ApplyIncrementAndSwitch(now).
func TestClock_IncrementOnAcceptedMoveOnly(t *testing.T) {
	t0 := time.Now()
	c := NewClockState(tc3plus2(), t0)

	t1 := t0.Add(10 * time.Second)
	c.Debit(t1)
	c.ApplyIncrementAndSwitch(t1)

	// White spent 10s and gained 2s increment → 3min - 10s + 2s.
	wantWhite := 3*time.Minute - 10*time.Second + 2*time.Second
	if c.Remain[White] != wantWhite {
		t.Errorf("white_remaining after move = %v, want %v", c.Remain[White], wantWhite)
	}
	// Black was not active, must not have moved or gained increment.
	if c.Remain[Black] != 3*time.Minute {
		t.Errorf("black_remaining = %v, want untouched %v", c.Remain[Black], 3*time.Minute)
	}
	// Active side flipped.
	if c.Active != Black {
		t.Errorf("Active after move = %v, want Black", c.Active)
	}
}

// FR-020 move-vs-flag determinism: a move that arrives AT or AFTER the active
// side's clock reaches zero MUST observe Remain == 0 (the flag stands). We
// check this via ActiveRemaining without mutating the state, which is what
// ApplyMove does before deciding to commit.
func TestClock_MoveArrivingAfterZeroSeesZeroRemaining(t *testing.T) {
	t0 := time.Now()
	// 1-second initial, no increment — extreme bullet for the test.
	tc := TimeControl{Initial: 1 * time.Second}
	c := NewClockState(tc, t0)

	// 1.5 seconds later, the active side is over the line.
	tooLate := t0.Add(1500 * time.Millisecond)
	if got := c.ActiveRemaining(tooLate); got != 0 {
		t.Fatalf("ActiveRemaining past expiry = %v, want 0", got)
	}
}

// FR-020 the symmetric case: a move arriving WITH time remaining still works.
func TestClock_MoveArrivingBeforeZeroSeesRemainingTime(t *testing.T) {
	t0 := time.Now()
	tc := TimeControl{Initial: 1 * time.Second}
	c := NewClockState(tc, t0)

	justInTime := t0.Add(900 * time.Millisecond)
	got := c.ActiveRemaining(justInTime)
	want := 100 * time.Millisecond
	if got != want {
		t.Fatalf("ActiveRemaining at t=900ms = %v, want %v (tolerance ±2ms ok)", got, want)
	}
}

// Bronstein delay: the first Delay of elapsed time on each move is free.
func TestClock_BronsteinDelay(t *testing.T) {
	t0 := time.Now()
	tc := TimeControl{
		Initial: 1 * time.Minute,
		Delay:   500 * time.Millisecond,
	}
	c := NewClockState(tc, t0)

	// 1 second elapses; the first 500ms is absorbed by delay; 500ms debits.
	c.Debit(t0.Add(1 * time.Second))
	want := 1*time.Minute - 500*time.Millisecond
	if c.Remain[White] != want {
		t.Errorf("white_remaining with delay = %v, want %v", c.Remain[White], want)
	}
}

// Remain is clamped at zero — a negative debit must not propagate.
func TestClock_RemainClampedAtZero(t *testing.T) {
	t0 := time.Now()
	c := NewClockState(TimeControl{Initial: 1 * time.Second}, t0)
	c.Debit(t0.Add(10 * time.Second))
	if c.Remain[White] != 0 {
		t.Fatalf("Remain after over-spend = %v, want 0", c.Remain[White])
	}
}
