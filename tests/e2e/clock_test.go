// Package e2e — clock / flag-on-time scaffold.
//
// Spec coverage:
//   User Story 2 (player flags on time).
//   FR-015 server is sole timing authority, FR-016 monotonic source,
//   FR-017 increment only on accepted move, FR-018 automatic flag,
//   FR-019 flag emitted within one short timer interval,
//   FR-020 deterministic move-vs-flag race resolution.
//   SC-005 flag adjudicated within 100ms of zero crossing.
package e2e

import (
	"testing"
	"time"
)

func TestE2E_FlagOnTime(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(US2, FR-015..FR-018): create a game with a tiny initial time
	// per side (e.g., 300ms White, 300ms Black, increment 0). Do NOT
	// submit any moves; let White's clock run out.
	// srv := startServer(t)
	// gameID := randomGameID()

	// TODO(FR-021): open an SSE subscriber and record the wall-clock time
	// of the first move into "active": "white" notification, then wait
	// for the terminal event.
	// start := time.Now()

	// TODO(FR-018, FR-019, SC-005): expect a terminal SSE event
	// (Outcome == BlackWin via flag) to arrive within ~400ms of `start`
	// (300ms initial + <=100ms adjudication budget per SC-005).
	deadline := 400 * time.Millisecond
	_ = deadline

	// TODO(FR-009 + game-over): assert a subsequent move POST returns
	// ErrGameOver and Snapshot reflects the flagged terminal state.

	// TODO(FR-020): negative case — re-run with White submitting a legal
	// move at ~250ms; expect the move to be accepted (no flag), seq
	// advances, and clock continues. Tight, so keep this branch behind
	// a `testing.Short()` guard if it proves flaky on CI.
}
