// Package e2e — optimistic concurrency + idempotency scaffold.
//
// Spec coverage:
//   FR-013 same client_action_id => single applied move, identical result.
//   FR-014 at most one transition commits per seq.
//   SC-006 retried move with same idempotency key => exactly one applied
//   move in 100% of retry cases.
//   SC-007 two concurrent submissions on same prior seq => exactly one
//   committed, the other gets ErrStaleSequence in 100% of cases.
package e2e

import (
	"testing"
)

func TestE2E_StaleSequenceRejected(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(FR-014, SC-007): create a game, then fire two POST /move
	// requests in parallel goroutines, both referencing seq=0 but with
	// different UCI moves and different client_action_id values.
	// srv := startServer(t)
	// gameID := randomGameID()

	// TODO(SC-007): collect both responses. Exactly one MUST be a
	// success (200 / ActionResult.Seq == 1). The other MUST be a
	// stale-sequence response (the reference treats 409 as acceptable
	// for stale-seq) carrying the latest snapshot for client reconcile.

	// TODO(FR-011): after the race, GET /games/{id}/snapshot and assert
	// Seq == 1 (advanced by exactly one despite two attempts).
}

func TestE2E_IdempotentRetry(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(FR-013, SC-006): create a game; submit a legal move with
	// client_action_id == "cmid-1" and capture the ActionResult.
	// srv := startServer(t)
	// gameID := randomGameID()
	// body := buildMoveReq(gameID, 0, "e2e4", "cmid-1")

	// TODO(FR-013): resubmit the IDENTICAL body. Server must return the
	// same ActionResult (same Seq, same Snapshot.FEN) and MUST NOT apply a
	// second move. GET /snapshot afterwards must still show Seq == 1.

	// TODO(SC-006): hammer the same idempotency key from K parallel
	// goroutines (e.g., K=20). All K responses MUST be successes
	// describing the single applied move; final Seq == 1.
}
