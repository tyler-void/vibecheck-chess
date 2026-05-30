package chess

import (
	"context"
	"testing"
	"time"
)

// FR-007: en passant legality. The capture is only legal on the move
// IMMEDIATELY following a two-square pawn advance that puts the opposing
// pawn adjacent. One move later, the en-passant target square is gone.

func TestEnPassant_LegalImmediately(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("ep1", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	seq := uint64(0)
	play := func(p PlayerID, uci string) {
		t.Helper()
		req := MoveReq{PlayerID: p, Move: uci, ClientActionID: uci, Seq: seq}
		if _, err := r.ApplyMove(context.Background(), req, now); err != nil {
			t.Fatalf("move %s by %s failed: %v", uci, p, err)
		}
		seq++
	}

	// Set up: white pawn on e5 ready to take black's d-pawn after d7-d5.
	play("w", "e2e4")
	play("b", "a7a6")
	play("w", "e4e5")
	play("b", "d7d5") // two-square advance — opens en-passant target on d6
	// White takes en passant: pawn from e5 captures to d6.
	play("w", "e5d6")

	snap := r.Snapshot(now)
	// After e.p. capture, d6 should hold a white pawn.
	// FEN encodes piece placement; the 6th rank from the FEN tells us.
	// Simpler: just verify the move was accepted and seq advanced.
	if snap.Seq != 5 {
		t.Errorf("seq after e.p. = %d, want 5", snap.Seq)
	}
}

// FR-007: en passant the move AFTER the two-square advance is illegal.
func TestEnPassant_NoLongerLegalOneMoveLater(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("ep2", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	seq := uint64(0)
	play := func(p PlayerID, uci string, mustFail error) {
		t.Helper()
		req := MoveReq{PlayerID: p, Move: uci, ClientActionID: uci, Seq: seq}
		_, err := r.ApplyMove(context.Background(), req, now)
		if mustFail != nil {
			if err != mustFail {
				t.Fatalf("move %s by %s: got %v, want %v", uci, p, err, mustFail)
			}
			return
		}
		if err != nil {
			t.Fatalf("move %s by %s failed: %v", uci, p, err)
		}
		seq++
	}

	play("w", "e2e4", nil)
	play("b", "a7a6", nil)
	play("w", "e4e5", nil)
	play("b", "d7d5", nil)
	play("w", "h2h3", nil) // White passes on e.p.
	play("b", "h7h6", nil)
	// Now attempt e5d6 — must be illegal (e.p. opportunity has expired).
	play("w", "e5d6", ErrIllegalMove)
}
