package chess

import (
	"context"
	"testing"
	"time"

	libchess "github.com/corentings/chess/v2"
)

// FR-009: insufficient material → auto-draw with Termination=InsufficientMaterial.
//
// Positions classically declared "insufficient material" by FIDE 5.2.2:
//   - King vs King
//   - King + Bishop vs King
//   - King + Knight vs King
//   - King + Bishop(s) vs King + Bishop(s) on same-colour squares
//
// We test K-vs-K. The corentings library detects it; we verify our adapter
// surfaces InsufficientMaterial as the termination.

func TestInsufficientMaterial_KingVsKing(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("im1", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	// Start directly from a K-vs-K position. The library only triggers
	// auto-detection of insufficient material when a Move is pushed, so we
	// drive a harmless white king move and let the library declare the
	// draw on the resulting position.
	fen := "8/8/8/3k4/8/8/8/4K3 w - - 0 1"
	opt, err := libchess.FEN(fen)
	if err != nil {
		t.Fatalf("FEN: %v", err)
	}
	r.game = libchess.NewGame(opt)
	r.repetition = map[uint64]uint8{positionKey(r.game.Position()): 1}

	req := MoveReq{PlayerID: "w", Move: "e1d1", ClientActionID: "kmove", Seq: 0}
	if _, err := r.ApplyMove(context.Background(), req, now); err != nil {
		t.Fatalf("Kd1 failed: %v", err)
	}

	snap := r.Snapshot(now)
	if snap.Outcome != Draw {
		t.Errorf("outcome = %v, want Draw", snap.Outcome)
	}
	if snap.Termination != InsufficientMaterial {
		t.Errorf("termination = %v, want InsufficientMaterial (got %v)", snap.Termination, snap.Termination)
	}
}
