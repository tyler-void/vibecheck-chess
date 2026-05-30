package chess

import (
	"context"
	"testing"
	"time"
)

// FR-007: castling legality.
//
// Kingside-castle sequence: clear the f1 and g1 squares (knight + bishop),
// then e1g1. Once the king has moved, kingside castle is illegal even if
// we tried to set up the position again.

func TestCastling_KingsideLegal(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("cs1", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	seq := uint64(0)
	play := func(player PlayerID, uci string) {
		t.Helper()
		_, err := r.ApplyMove(context.Background(), MoveReq{
			PlayerID:       player,
			Move:           uci,
			ClientActionID: uci,
			Seq:            seq,
		}, now)
		if err != nil {
			t.Fatalf("move %s by %s failed: %v", uci, player, err)
		}
		seq++
	}

	// Italian-ish development clearing f1/g1 for White and f8/g8 for Black.
	play("w", "e2e4")
	play("b", "e7e5")
	play("w", "g1f3")
	play("b", "g8f6")
	play("w", "f1e2")
	play("b", "f8e7")
	// White kingside castle.
	play("w", "e1g1")

	snap := r.Snapshot(now)
	if snap.Outcome != Ongoing {
		t.Errorf("after castle outcome = %v, want Ongoing", snap.Outcome)
	}
}

// FR-008: client cannot bypass castling rights by lying about position;
// the server validates. After the king has moved (and moved back), kingside
// castle MUST be rejected as illegal.
func TestCastling_AfterKingMoveIllegal(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("cs2", "w", "b", TimeControl{Initial: 5 * time.Minute}, now)

	moves := []struct {
		p PlayerID
		m string
	}{
		{"w", "e2e4"}, {"b", "e7e5"},
		{"w", "g1f3"}, {"b", "g8f6"},
		{"w", "f1e2"}, {"b", "f8e7"},
		{"w", "e1f1"}, // king moves — castling rights lost
		{"b", "d7d6"},
		{"w", "f1e1"}, // king returns
		{"b", "d8d7"},
	}
	for i, mv := range moves {
		req := MoveReq{PlayerID: mv.p, Move: mv.m, ClientActionID: mv.m, Seq: uint64(i)}
		if _, err := r.ApplyMove(context.Background(), req, now); err != nil {
			t.Fatalf("setup move %d (%s) failed: %v", i, mv.m, err)
		}
	}
	// Now attempt kingside castle — must be illegal because the king has moved.
	req := MoveReq{PlayerID: "w", Move: "e1g1", ClientActionID: "post-king-move", Seq: 10}
	if _, err := r.ApplyMove(context.Background(), req, now); err != ErrIllegalMove {
		t.Errorf("castle after king moved returned %v, want ErrIllegalMove", err)
	}
}
