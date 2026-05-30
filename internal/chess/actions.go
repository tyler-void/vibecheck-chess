package chess

import (
	"context"
	"time"

	"kisschess/internal/dsenc"
)

// ApplyResign terminates the game with the submitting player losing. FR-036.
// Same lock discipline as ApplyMove: parse-outside / lock / mutate / unlock /
// broadcast (Constitution III).
//
// Idempotent retry via req.ClientActionID — a repeated submission returns
// the cached ActionResult without re-broadcasting.
//
// Errors:
//
//   - ErrNotParticipant — req.PlayerID isn't White or Black.
//   - ErrStaleSequence  — req.Seq != current seq.
//   - ErrGameOver       — Outcome already set.
func (r *GameRoom) ApplyResign(ctx context.Context, req ActionReq, now time.Time) (ActionResult, error) {
	start := time.Now()
	r.mu.Lock()

	if cached, ok := r.seenActions[req.ClientActionID]; ok {
		r.mu.Unlock()
		cached.Idempotent = true
		return cached, nil
	}

	color, ok := r.playerColor(req.PlayerID)
	if !ok {
		r.mu.Unlock()
		return ActionResult{}, ErrNotParticipant
	}

	if r.outcome != Ongoing {
		r.mu.Unlock()
		return ActionResult{}, ErrGameOver
	}

	if req.Seq != r.seq {
		r.mu.Unlock()
		return ActionResult{}, ErrStaleSequence
	}

	// The resigning side loses; opponent wins.
	if color == White {
		r.outcome = BlackWin
	} else {
		r.outcome = WhiteWin
	}
	r.termination = Resignation
	r.drawOfferActive = false // terminal nullifies any pending offer
	if r.flagTimer != nil {
		r.flagTimer.Stop()
		r.flagTimer = nil
	}
	r.seq++

	snap := r.snapshotLocked(now)
	bytes := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
	r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: bytes})

	result := ActionResult{Seq: snap.Seq, Snapshot: snap}
	r.rememberAction(req.ClientActionID, result)

	r.mu.Unlock()
	r.hub.Publish(bytes)
	recordAction(ActionResign, start)
	logActionAccepted(ActionResign, r.id, result.Seq)
	logTerminal(r.id, result.Snapshot.Outcome, result.Snapshot.Termination, result.Seq)
	return result, nil
}

// ApplyDrawOffer places (or replaces) the submitting player's pending draw
// offer. FR-037. Adds 1 to seq and broadcasts a signal patch so the
// opponent's UI surfaces the affordance.
//
// Errors: ErrNotParticipant, ErrStaleSequence, ErrGameOver.
func (r *GameRoom) ApplyDrawOffer(ctx context.Context, req ActionReq, now time.Time) (ActionResult, error) {
	start := time.Now()
	r.mu.Lock()

	if cached, ok := r.seenActions[req.ClientActionID]; ok {
		r.mu.Unlock()
		cached.Idempotent = true
		return cached, nil
	}

	color, ok := r.playerColor(req.PlayerID)
	if !ok {
		r.mu.Unlock()
		return ActionResult{}, ErrNotParticipant
	}

	if r.outcome != Ongoing {
		r.mu.Unlock()
		return ActionResult{}, ErrGameOver
	}

	if req.Seq != r.seq {
		r.mu.Unlock()
		return ActionResult{}, ErrStaleSequence
	}

	// A new offer from the same side replaces any prior pending offer.
	r.drawOfferActive = true
	r.drawOfferFrom = color
	r.drawOfferAtSeq = r.seq
	r.seq++

	snap := r.snapshotLocked(now)
	bytes := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
	r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: bytes})

	result := ActionResult{Seq: snap.Seq, Snapshot: snap}
	r.rememberAction(req.ClientActionID, result)

	r.mu.Unlock()
	r.hub.Publish(bytes)
	recordAction(ActionDrawOffer, start)
	logActionAccepted(ActionDrawOffer, r.id, result.Seq)
	return result, nil
}

// ApplyDrawAccept ends the game as an agreed draw. The acceptor must NOT be
// the originator of the pending offer (ErrOwnOffer); a pending offer must
// exist (ErrNoPendingOffer).
//
// Errors: ErrNotParticipant, ErrNoPendingOffer, ErrOwnOffer,
// ErrStaleSequence, ErrGameOver.
func (r *GameRoom) ApplyDrawAccept(ctx context.Context, req ActionReq, now time.Time) (ActionResult, error) {
	start := time.Now()
	r.mu.Lock()

	if cached, ok := r.seenActions[req.ClientActionID]; ok {
		r.mu.Unlock()
		cached.Idempotent = true
		return cached, nil
	}

	color, ok := r.playerColor(req.PlayerID)
	if !ok {
		r.mu.Unlock()
		return ActionResult{}, ErrNotParticipant
	}

	if r.outcome != Ongoing {
		r.mu.Unlock()
		return ActionResult{}, ErrGameOver
	}

	if req.Seq != r.seq {
		r.mu.Unlock()
		return ActionResult{}, ErrStaleSequence
	}

	if !r.drawOfferActive {
		r.mu.Unlock()
		return ActionResult{}, ErrNoPendingOffer
	}
	if r.drawOfferFrom == color {
		r.mu.Unlock()
		return ActionResult{}, ErrOwnOffer
	}

	r.outcome = Draw
	r.termination = AgreedDraw
	r.drawOfferActive = false
	if r.flagTimer != nil {
		r.flagTimer.Stop()
		r.flagTimer = nil
	}
	r.seq++

	snap := r.snapshotLocked(now)
	bytes := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
	r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: bytes})

	result := ActionResult{Seq: snap.Seq, Snapshot: snap}
	r.rememberAction(req.ClientActionID, result)

	r.mu.Unlock()
	r.hub.Publish(bytes)
	recordAction(ActionDrawAccept, start)
	logActionAccepted(ActionDrawAccept, r.id, result.Seq)
	logTerminal(r.id, result.Snapshot.Outcome, result.Snapshot.Termination, result.Seq)
	return result, nil
}
