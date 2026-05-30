// Package e2e — reconnect / Last-Event-ID scaffold.
//
// Spec coverage:
//   User Story 4 (viewer reconnects and catches up).
//   FR-024 bounded per-room event ring buffer for replay,
//   FR-025 deliver in-order missed events OR a fresh full snapshot when
//   the gap is too large or no starting point is supplied.
package e2e

import (
	"testing"
)

func TestE2E_LastEventIDReplay(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(US4, FR-024): open SSE, observe events up to seq=N, close.
	// While disconnected, commit a small number of additional moves
	// (still within the ring buffer capacity).
	// srv := startServer(t)
	// gameID := randomGameID()

	// TODO(FR-024, FR-025): reconnect with header Last-Event-ID: N.
	// Expect to receive each missed event in order with ids N+1, N+2,...
	// and NO full-snapshot frame. parseSSE should yield contiguous ids.

	// TODO(edge): reconnect when already up-to-date — server emits only
	// future events (no replay, no snapshot, just keepalives until the
	// next move).
}

func TestE2E_LastEventIDSnapshotFallback(t *testing.T) {
	t.Skip("scaffold: awaiting src/server implementation")

	// TODO(US4, FR-025): open SSE, close, then commit MORE moves than the
	// per-room ring buffer holds so the gap is unrecoverable.
	// srv := startServer(t)
	// gameID := randomGameID()

	// TODO(FR-025): reconnect with the now-too-old Last-Event-ID. Expect
	// a single full-snapshot frame (BoardSnapshot serialized) before
	// live updates resume.

	// TODO(edge from spec §Edge Cases): also exercise reconnect with a
	// malformed Last-Event-ID (non-numeric); server treats it as a
	// fresh connection and delivers a snapshot.
}
