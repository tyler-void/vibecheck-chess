// Package e2e — game flow scaffold.
//
// Spec coverage:
//   User Story 1 (two-player authoritative game).
//   FR-001 game creation, FR-005 UCI move input, FR-006 turn enforcement,
//   FR-007 full chess legality, FR-009 terminal adjudication,
//   FR-011 monotonic seq, FR-021/022 per-game broadcast, FR-023 seq tag.
//
// Intent: drive a short forced mate (e.g., Fool's Mate: f2f3, e7e5, g2g4,
// d8h4#) end-to-end, observing that every accepted move increments seq by
// exactly one and that both connected SSE clients receive each event in
// order. Verify that an out-of-turn move and an illegal move are rejected.
package e2e

import (
	"testing"
)

func TestE2E_GameFlow_TwoPlayerCheckmate(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(US1, FR-001): create game with two players + time control.
	// srv := startServer(t)
	// gameID := randomGameID()

	// TODO(FR-005, FR-006, FR-007, FR-011): play Fool's Mate.
	// moves := []string{"f2f3", "e7e5", "g2g4", "d8h4"}
	// for i, mv := range moves {
	//     body := buildMoveReq(gameID, uint64(i), mv, "cmid-"+mv)
	//     resp, err := postMove(srv, gameID, body)
	//     if err != nil { t.Fatal(err) }
	//     // assert 200 and resp.Snapshot.Seq == i+1
	//     _ = resp
	// }

	// TODO(FR-006): assert that Black playing on White's turn returns
	// ErrNotYourTurn (HTTP error mapped by writeMoveResponse).

	// TODO(FR-007): assert that an illegal move (e.g., "e2e5" from start)
	// returns ErrIllegalMove and seq is unchanged.

	// TODO(FR-009): after the mating move, assert Outcome == BlackWin and
	// any further move is rejected with ErrGameOver.

	// TODO(FR-021, FR-022, FR-023): open two SSE subscribers before the
	// first move; verify each receives 4 events in order with ids 1..4
	// and event: board. Use parseSSE to drain the stream.
}
