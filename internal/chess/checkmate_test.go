package chess

import (
	"context"
	"testing"
	"time"
)

// FR-007 full chess legality · FR-009 terminal adjudication ·
// Spec US1 acceptance scenario 6.
//
// The canonical fastest mate in chess — Fool's Mate — exercises move
// acceptance, turn switching, and the Checkmate termination path in one
// 4-ply sequence. Per Constitution VI this test was authored before the
// ApplyMove implementation it drives.

func TestCheckmate_FoolsMate(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	tc := TimeControl{Initial: 10 * time.Minute, Increment: 0}
	r := NewGameRoom("g1", "alice", "bob", tc, now)

	moves := []struct {
		player PlayerID
		uci    string
	}{
		{"alice", "f2f3"},
		{"bob", "e7e5"},
		{"alice", "g2g4"},
		{"bob", "d8h4"},
	}

	for i, m := range moves {
		req := MoveReq{
			PlayerID:       m.player,
			Move:           m.uci,
			ClientActionID: "fm-" + m.uci,
			Seq:            uint64(i),
		}
		res, err := r.ApplyMove(context.Background(), req, now.Add(time.Duration(i+1)*time.Second))
		if err != nil {
			t.Fatalf("move %d (%s by %s) failed: %v", i, m.uci, m.player, err)
		}
		if res.Seq != uint64(i+1) {
			t.Errorf("move %d: result.Seq = %d, want %d", i, res.Seq, i+1)
		}
	}

	snap := r.Snapshot(now.Add(5 * time.Second))
	if snap.Outcome != BlackWin {
		t.Errorf("final outcome = %v, want BlackWin", snap.Outcome)
	}
	if snap.Termination != Checkmate {
		t.Errorf("final termination = %v, want Checkmate", snap.Termination)
	}

	// FR-009: no further moves accepted after terminal.
	postReq := MoveReq{
		PlayerID:       "alice",
		Move:           "e2e4",
		ClientActionID: "post-terminal",
		Seq:            4,
	}
	if _, err := r.ApplyMove(context.Background(), postReq, now.Add(6*time.Second)); err == nil {
		t.Errorf("move after checkmate was accepted; want ErrGameOver")
	}
}

// FR-006 turn enforcement: Black cannot move on move 1.
func TestCheckmate_OutOfTurnRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	tc := TimeControl{Initial: 10 * time.Minute}
	r := NewGameRoom("g2", "alice", "bob", tc, now)

	req := MoveReq{
		PlayerID:       "bob", // Black tries to move first
		Move:           "e7e5",
		ClientActionID: "x1",
		Seq:            0,
	}
	if _, err := r.ApplyMove(context.Background(), req, now); err != ErrNotYourTurn {
		t.Errorf("Black-on-move-1 returned %v, want ErrNotYourTurn", err)
	}
}

// FR-007: an illegal move is rejected (e.g., pawn jumping two from a non-2/7 rank).
func TestCheckmate_IllegalMoveRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	tc := TimeControl{Initial: 10 * time.Minute}
	r := NewGameRoom("g3", "alice", "bob", tc, now)

	req := MoveReq{
		PlayerID:       "alice",
		Move:           "e2e5", // pawn jumps too far
		ClientActionID: "ill",
		Seq:            0,
	}
	if _, err := r.ApplyMove(context.Background(), req, now); err != ErrIllegalMove {
		t.Errorf("e2e5 from start returned %v, want ErrIllegalMove", err)
	}
}

// FR-013 idempotency: re-submitting the same client_action_id returns the
// cached result and does NOT advance seq.
func TestCheckmate_IdempotentRetryReturnsCached(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	tc := TimeControl{Initial: 10 * time.Minute}
	r := NewGameRoom("g4", "alice", "bob", tc, now)

	req := MoveReq{PlayerID: "alice", Move: "e2e4", ClientActionID: "k1", Seq: 0}
	first, err := r.ApplyMove(context.Background(), req, now)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if first.Seq != 1 {
		t.Fatalf("first.Seq = %d, want 1", first.Seq)
	}

	// Retry with the same key — should return identical cached result.
	second, err := r.ApplyMove(context.Background(), req, now.Add(time.Second))
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if second.Seq != first.Seq {
		t.Errorf("retry advanced seq from %d to %d; want no advance", first.Seq, second.Seq)
	}
	if second.Snapshot.FEN != first.Snapshot.FEN {
		t.Errorf("retry produced different FEN; cache lied")
	}
}

// FR-012 / SC-007: a stale seq is rejected.
func TestCheckmate_StaleSeqRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	tc := TimeControl{Initial: 10 * time.Minute}
	r := NewGameRoom("g5", "alice", "bob", tc, now)

	// First move advances seq to 1.
	_, _ = r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "ok", Seq: 0,
	}, now)

	stale := MoveReq{PlayerID: "bob", Move: "e7e5", ClientActionID: "stale", Seq: 0} // wrong seq
	if _, err := r.ApplyMove(context.Background(), stale, now.Add(time.Second)); err != ErrStaleSequence {
		t.Errorf("stale seq returned %v, want ErrStaleSequence", err)
	}
}
