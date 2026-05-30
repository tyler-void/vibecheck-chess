package chess

import (
	"errors"
	"time"
)

// TimeControl is the configured timing for a game. Immutable once a room is
// created. Asymmetric increments (different per side) are allowed by the
// underlying ClockState model but the public TimeControl uses a single
// Increment applied to both sides — symmetry is the common chess convention
// and what the HTTP create-game contract exposes today.
type TimeControl struct {
	Initial   time.Duration // per side; > 0
	Increment time.Duration // per move on the moving side; ≥ 0
	Delay     time.Duration // Bronstein-style delay before the clock starts ticking; ≥ 0
}

// Validate enforces the rules from contracts/http-api.md `POST /games`.
// Returns an error suitable for surfacing as an invalid_request reason.
func (tc TimeControl) Validate() error {
	if tc.Initial <= 0 {
		return errors.New("time_control.initial must be > 0")
	}
	if tc.Increment < 0 {
		return errors.New("time_control.increment must be >= 0")
	}
	if tc.Delay < 0 {
		return errors.New("time_control.delay must be >= 0")
	}
	return nil
}
