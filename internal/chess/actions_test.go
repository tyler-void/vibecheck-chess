package chess

import (
	"context"
	"testing"
	"time"
)

// FR-036 resign + FR-037 draw offer / accept lifecycle.

func TestResign_OpponentWins(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("rs1", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)

	res, err := r.ApplyResign(context.Background(), ActionReq{
		PlayerID:       "alice",
		ClientActionID: "r1",
		Seq:            0,
	}, now)
	if err != nil {
		t.Fatalf("ApplyResign failed: %v", err)
	}
	if res.Snapshot.Outcome != BlackWin {
		t.Errorf("after white resigns, outcome = %v, want BlackWin", res.Snapshot.Outcome)
	}
	if res.Snapshot.Termination != Resignation {
		t.Errorf("termination = %v, want Resignation", res.Snapshot.Termination)
	}
	if res.Seq != 1 {
		t.Errorf("seq after resign = %d, want 1", res.Seq)
	}
}

func TestResign_NotParticipantRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("rs2", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)
	_, err := r.ApplyResign(context.Background(), ActionReq{
		PlayerID: "carol", ClientActionID: "r1", Seq: 0,
	}, now)
	if err != ErrNotParticipant {
		t.Errorf("non-participant resign got %v, want ErrNotParticipant", err)
	}
}

func TestResign_PostTerminalRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("rs3", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)
	_, _ = r.ApplyResign(context.Background(), ActionReq{PlayerID: "alice", ClientActionID: "r1", Seq: 0}, now)
	// Game is over; second resign rejected.
	_, err := r.ApplyResign(context.Background(), ActionReq{PlayerID: "bob", ClientActionID: "r2", Seq: 1}, now)
	if err != ErrGameOver {
		t.Errorf("post-terminal resign got %v, want ErrGameOver", err)
	}
}

// FR-037: offer + accept lifecycle.
func TestDrawOffer_AcceptedByOpponent(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("do1", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)

	// Alice (white) offers.
	res, err := r.ApplyDrawOffer(context.Background(), ActionReq{
		PlayerID: "alice", ClientActionID: "o1", Seq: 0,
	}, now)
	if err != nil {
		t.Fatalf("ApplyDrawOffer failed: %v", err)
	}
	if res.Snapshot.DrawOfferFrom != "w" {
		t.Errorf("draw_offer_from = %q, want %q", res.Snapshot.DrawOfferFrom, "w")
	}

	// Bob accepts.
	res2, err := r.ApplyDrawAccept(context.Background(), ActionReq{
		PlayerID: "bob", ClientActionID: "a1", Seq: 1,
	}, now)
	if err != nil {
		t.Fatalf("ApplyDrawAccept failed: %v", err)
	}
	if res2.Snapshot.Outcome != Draw {
		t.Errorf("outcome = %v, want Draw", res2.Snapshot.Outcome)
	}
	if res2.Snapshot.Termination != AgreedDraw {
		t.Errorf("termination = %v, want AgreedDraw", res2.Snapshot.Termination)
	}
	if res2.Snapshot.DrawOfferFrom != "" {
		t.Errorf("draw_offer_from after accept = %q, want empty", res2.Snapshot.DrawOfferFrom)
	}
}

// FR-037: accepting your own offer is rejected.
func TestDrawOffer_OwnOfferRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("do2", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)
	_, _ = r.ApplyDrawOffer(context.Background(), ActionReq{PlayerID: "alice", ClientActionID: "o1", Seq: 0}, now)
	_, err := r.ApplyDrawAccept(context.Background(), ActionReq{PlayerID: "alice", ClientActionID: "a1", Seq: 1}, now)
	if err != ErrOwnOffer {
		t.Errorf("accepting own offer got %v, want ErrOwnOffer", err)
	}
}

// FR-037: accept without a pending offer is rejected.
func TestDrawOffer_AcceptWithoutPendingRejected(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("do3", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)
	_, err := r.ApplyDrawAccept(context.Background(), ActionReq{PlayerID: "bob", ClientActionID: "a1", Seq: 0}, now)
	if err != ErrNoPendingOffer {
		t.Errorf("no-pending-offer accept got %v, want ErrNoPendingOffer", err)
	}
}

// FR-037: a move by the offering side withdraws the pending offer.
func TestDrawOffer_WithdrawnByOfferingSideMove(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	r := NewGameRoom("do4", "alice", "bob", TimeControl{Initial: 5 * time.Minute}, now)

	_, _ = r.ApplyDrawOffer(context.Background(), ActionReq{PlayerID: "alice", ClientActionID: "o1", Seq: 0}, now)
	// Alice now moves — pending offer should auto-withdraw.
	_, err := r.ApplyMove(context.Background(), MoveReq{
		PlayerID: "alice", Move: "e2e4", ClientActionID: "m1", Seq: 1,
	}, now)
	if err != nil {
		t.Fatalf("Alice's move after her own offer failed: %v", err)
	}
	snap := r.Snapshot(now)
	if snap.DrawOfferFrom != "" {
		t.Errorf("after Alice moves, draw_offer_from = %q, want empty (withdrawn)", snap.DrawOfferFrom)
	}
	// Bob now tries to accept — should fail with ErrNoPendingOffer.
	_, err = r.ApplyDrawAccept(context.Background(), ActionReq{PlayerID: "bob", ClientActionID: "a1", Seq: 2}, now)
	if err != ErrNoPendingOffer {
		t.Errorf("post-withdrawal accept got %v, want ErrNoPendingOffer", err)
	}
}
