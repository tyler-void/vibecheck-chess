package chess

import (
	"testing"
	"time"

	libchess "github.com/corentings/chess/v2"
)

// FR-009: stalemate produces Outcome=Draw with Termination=Stalemate.
//
// We don't drive a long stalemate sequence from the starting position —
// instead we instantiate a known stalemate position via FEN, then push a
// move that triggers the stalemate detection. This keeps the test small
// and the intent obvious.

func TestStalemate_KnownPositionDeclaredDraw(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("st1", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	// Replace the underlying game with a near-stalemate FEN. Sequence:
	//   White: Kf6, Qg6. Black: Kh8. White to move with Qg7# = mate; we want
	//   a stalemate variant: White Kf7, Qg6, Black Kh8.
	//
	// The position "k7/8/1Q6/8/8/8/8/K7 b - - 0 1" gives Black king on a8,
	// White queen b6, White king a1, Black to move — Black is stalemated.
	fen := "k7/8/1Q6/8/8/8/8/K7 b - - 0 1"
	opt, err := libchess.FEN(fen)
	if err != nil {
		t.Fatalf("FEN setup failed: %v", err)
	}
	r.game = libchess.NewGame(opt)

	// Now the position itself IS stalemate. updateDrawStateLocked detects
	// it on the next move attempt — but Black has no legal moves, so we
	// can't drive a "make a move" path. Instead just call the detection
	// directly under the lock.
	r.mu.Lock()
	r.updateDrawStateLocked(false)
	r.mu.Unlock()

	snap := r.Snapshot(now)
	if snap.Outcome != Draw {
		t.Errorf("outcome = %v, want Draw", snap.Outcome)
	}
	if snap.Termination != Stalemate {
		t.Errorf("termination = %v, want Stalemate", snap.Termination)
	}
}
