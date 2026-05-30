package chess

import (
	"context"
	"testing"
	"time"
)

// FR-005 / FR-007: promotion legality.
//
//   - Accept `q|r|b|n` suffix.
//   - Reject missing suffix on a promotion move.
//   - Reject `k|p` suffix or any other letter.
//   - Reject suffix on a non-promotion move (e2e4q).

// Use a hand-built mid-game (e2e4 only fast path) — we just need a position
// where a White pawn can promote on its next move. We get there by playing
// out a short opening then pushing a white pawn to the 7th rank.

func TestPromotion_QueenSuffixAccepted(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("p1", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)
	// Position with a white pawn ready to promote on a7→a8.
	// Sequence: trade off some pieces then walk an a-pawn up.
	playLine(t, r, []string{
		"a2a4", "h7h6",
		"a4a5", "h6h5",
		"a5a6", "h5h4",
		"a6b7", "h4h3", // axb7
		// Capture rook at a8 so b7 can promote on b8.
	})
	// b7 -> b8 with queen promotion.
	req := MoveReq{
		PlayerID:       "w",
		Move:           "b7a8q",
		ClientActionID: "promote",
		Seq:            8,
	}
	if _, err := r.ApplyMove(context.Background(), req, now); err != nil {
		t.Fatalf("b7a8q promotion failed: %v", err)
	}
}

// Missing promotion suffix on a promotion move — corentings/chess rejects.
// We expect ErrIllegalMove (the library returns an error during decoding/
// validation; ApplyMove maps that to ErrIllegalMove).
func TestPromotion_MissingSuffixIllegal(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("p2", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)
	playLine(t, r, []string{
		"a2a4", "h7h6",
		"a4a5", "h6h5",
		"a5a6", "h5h4",
		"a6b7", "h4h3",
	})
	req := MoveReq{PlayerID: "w", Move: "b7a8", ClientActionID: "no-suffix", Seq: 8}
	if _, err := r.ApplyMove(context.Background(), req, now); err != ErrIllegalMove {
		t.Errorf("promotion without suffix returned %v, want ErrIllegalMove", err)
	}
}

// `k` (king) promotion suffix — not legal per the spec regex AND not legal
// per chess rules.
func TestPromotion_KingSuffixSyntaxRejected(t *testing.T) {
	// Our regex `^[a-h][1-8][a-h][1-8][qrbn]?$` rejects k/p outright.
	if err := ValidateUCISyntax("b7a8k"); err != ErrBadMoveSyntax {
		t.Errorf("b7a8k validate = %v, want ErrBadMoveSyntax", err)
	}
	if err := ValidateUCISyntax("b7a8p"); err != ErrBadMoveSyntax {
		t.Errorf("b7a8p validate = %v, want ErrBadMoveSyntax", err)
	}
}

// Promotion suffix on a non-promotion move (e2e4q) — the regex matches it
// syntactically (the suffix is optional, and the regex doesn't check
// whether the destination rank is 1/8). The chess engine MUST reject it as
// illegal.
func TestPromotion_SuffixOnNonPromotionMoveIllegal(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("p3", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)
	req := MoveReq{PlayerID: "w", Move: "e2e4q", ClientActionID: "bad-promo", Seq: 0}
	if _, err := r.ApplyMove(context.Background(), req, now); err != ErrIllegalMove {
		t.Errorf("e2e4q returned %v, want ErrIllegalMove", err)
	}
}

// Helper: play a sequence of UCI moves alternating w/b. Fails the test on
// any error.
func playLine(t *testing.T, r *GameRoom, moves []string) {
	t.Helper()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	for i, m := range moves {
		var p PlayerID = "w"
		if i%2 == 1 {
			p = "b"
		}
		req := MoveReq{PlayerID: p, Move: m, ClientActionID: m, Seq: uint64(i)}
		if _, err := r.ApplyMove(context.Background(), req, now); err != nil {
			t.Fatalf("setup move %d (%s by %s) failed: %v", i, m, p, err)
		}
	}
}
