// Package e2e — optimistic concurrency + idempotency.
//
// Spec coverage:
//   FR-013 same client_action_id => single applied move, identical result.
//   FR-014 at most one transition commits per seq.
//   SC-006 retried action with same idempotency key => exactly one applied
//   in 100% of retry cases.
//   SC-007 two concurrent submissions on same prior seq => exactly one
//   committed, the other gets ErrStaleSequence in 100% of cases.
package e2e

import (
	"net/http"
	"sync"
	"testing"
)

// FR-013 + SC-006: same client_action_id retried; only one applied.
func TestE2E_IdempotentRetry(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	body := buildMoveReq(0, "e2e4", "shared-key")
	// First submission — fresh accept → 204.
	r1 := postMove(t, srv, gameID, "alice", body)
	r1.Body.Close()
	if r1.StatusCode != http.StatusNoContent {
		t.Fatalf("first submit status=%d, want 204", r1.StatusCode)
	}
	// Retry with the same client_action_id → 200 OK (idempotent).
	r2 := postMove(t, srv, gameID, "alice", body)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("idempotent retry status=%d, want 200", r2.StatusCode)
	}
}

// FR-014 + SC-007: concurrent submissions on the same seq → exactly one
// commits, the other gets 409 stale_sequence.
func TestE2E_ConcurrentSameSeqOneWins(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	// Both clients try to move on seq=0 — only the FIRST through the lock
	// can commit; the other arrives to see seq=1 and gets stale.
	// Both submissions target the same legal move with DIFFERENT client_action_ids
	// so the cache can't dedupe; the seq check is the only gate.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := buildMoveReq(0, "e2e4", "conc-"+string(rune('a'+i)))
			r := postMove(t, srv, gameID, "alice", body)
			statuses[i] = r.StatusCode
			r.Body.Close()
		}()
	}
	wg.Wait()

	wins, stale := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusNoContent:
			wins++
		case http.StatusConflict:
			stale++
		default:
			t.Errorf("unexpected status=%d (want 204 or 409)", s)
		}
	}
	if wins != 1 {
		t.Errorf("wins=%d, want exactly 1", wins)
	}
	if stale != 1 {
		t.Errorf("stale=%d, want exactly 1", stale)
	}
}
