// Package e2e — spectator fan-out scaffold.
//
// Spec coverage:
//   User Story 3 (spectators stream a live game).
//   FR-021 per-game realtime stream, FR-022 broadcast only to that game's
//   subscribers.
//   SC-003 player-observed move latency with 1,000 spectators is no more
//   than 20% higher than with zero spectators on the same game.
package e2e

import (
	"testing"
)

func TestE2E_SpectatorFanout(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(US3, FR-021): create one game, open N spectator SSE
	// connections to /games/{id}/sse. N defaults small (e.g., 16) for
	// unit-test speed; load-test (1,000+) lives under tests/perf/.
	// srv := startServer(t)
	// gameID := randomGameID()
	// const N = 16

	// TODO(FR-022): submit a single legal move from White (e.g., "e2e4").
	// Every one of the N spectator streams MUST receive exactly one
	// board event with id == "1" and event == "board". Use parseSSE.

	// TODO(FR-022 isolation): create a second game on the same server
	// and verify that spectators of game A receive NOTHING when a move
	// is committed in game B.

	// TODO(SC-003): measure POST /move round-trip with 0 spectators vs N
	// spectators on the same game; assert the ratio stays below 1.2x.
	// Real perf gating belongs in k6/tests/perf, but a sanity check
	// here is cheap and catches obvious regressions.
}
