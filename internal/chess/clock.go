package chess

import "time"

// ClockState holds the live timing for one game. All times use Go's monotonic
// clock component (LastTick captures the monotonic instant of the last
// adjudication tick); arithmetic on time.Time values is immune to wall-clock
// jumps like NTP step adjustments. Constitution gate VI / research.md R-4.
//
// The flag timer (time.AfterFunc) lives on the room, not on ClockState — that
// keeps this struct pure data, easy to test in isolation, and lets the room
// own the timer lifecycle alongside its own seq and lock.
type ClockState struct {
	// Remain is per-side remaining time, indexed by Color. Mutated under
	// the room lock.
	Remain [2]time.Duration

	// Increment is added to the moving side AFTER their move is accepted.
	// Symmetric for now; the array shape keeps room for asymmetric controls.
	Increment [2]time.Duration

	// Delay is Bronstein-style — subtracted from elapsed before debiting.
	Delay time.Duration

	// LastTick is the monotonic instant of the last clock adjudication
	// (debit / increment-and-switch / flag-fire). NEVER set this from a
	// client-supplied timestamp.
	LastTick time.Time

	// Active is the side whose clock is currently running.
	Active Color
}

// NewClockState constructs a ClockState ready for the first move of the
// game. Both sides start with TimeControl.Initial; White is on the clock.
func NewClockState(tc TimeControl, now time.Time) ClockState {
	return ClockState{
		Remain:    [2]time.Duration{tc.Initial, tc.Initial},
		Increment: [2]time.Duration{tc.Increment, tc.Increment},
		Delay:     tc.Delay,
		LastTick:  now,
		Active:    White,
	}
}

// Debit accounts for time elapsed on the active side since LastTick.
//
// This is called at the START of every action that needs to know "how much
// time does the active side really have right now?" — including ApplyMove
// (FR-020 deterministic move-vs-flag), ApplyResign (FR-036), the flag-timer
// callback, and snapshot generation.
//
// Bronstein delay (when configured) absorbs the first c.Delay of elapsed time
// before any debit happens.
//
// Remain[Active] is clamped at zero — a negative remaining is a flagged
// condition, not a value we want to propagate downstream.
func (c *ClockState) Debit(now time.Time) {
	elapsed := now.Sub(c.LastTick)
	if c.Delay > 0 {
		elapsed -= c.Delay
		if elapsed < 0 {
			elapsed = 0
		}
	}
	c.Remain[c.Active] -= elapsed
	if c.Remain[c.Active] < 0 {
		c.Remain[c.Active] = 0
	}
	c.LastTick = now
}

// ApplyIncrementAndSwitch is called AFTER a legal move from c.Active has been
// accepted. Increment is credited to the side that just moved (FR-017), then
// the clock switches to the opponent.
//
// Precondition: c.Debit(now) was called immediately before; LastTick == now.
// The combined sequence — Debit(now) → ApplyIncrementAndSwitch(now) — is the
// canonical "move accepted" clock transition.
func (c *ClockState) ApplyIncrementAndSwitch(now time.Time) {
	mover := c.Active
	c.Remain[mover] += c.Increment[mover]
	c.Active = opposite(mover)
	c.LastTick = now
}

// ActiveRemaining returns the active side's remaining time AT THE TIME the
// caller is asking — i.e., after a notional Debit(now). It does NOT mutate
// c; use Debit if you also want to persist the new LastTick.
func (c ClockState) ActiveRemaining(now time.Time) time.Duration {
	elapsed := now.Sub(c.LastTick)
	if c.Delay > 0 {
		elapsed -= c.Delay
		if elapsed < 0 {
			elapsed = 0
		}
	}
	r := c.Remain[c.Active] - elapsed
	if r < 0 {
		return 0
	}
	return r
}
