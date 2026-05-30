package chess

import (
	"context"
	"time"

	libchess "github.com/corentings/chess/v2"

	"kisschess/internal/dsenc"
)

// MoveReq is the validated, server-side view of an incoming move action.
// Constructed by the HTTP layer after parsing the JSON body and resolving
// PlayerID from the auth header. The chess package never sees the raw HTTP
// request — it operates entirely on this typed struct.
type MoveReq struct {
	PlayerID       PlayerID
	Move           string // UCI long-algebraic, validated by ValidateUCISyntax
	ClientActionID string
	Seq            uint64
	ClaimDraw      bool
}

// ResignReq / DrawOfferReq / DrawAcceptReq share the same shape — only the
// move-specific Move + ClaimDraw fields differ. We use a single ActionReq
// for non-move actions to keep the apply.go signature stable.
type ActionReq struct {
	PlayerID       PlayerID
	ClientActionID string
	Seq            uint64
}

// ApplyMove is THE state-mutation entry point for a chess move. It implements
// the canonical sequence from Constitution III and FR-033:
//
//  1. PARSE — validate UCI syntax (already done by HTTP layer; we re-check
//     defensively).
//  2. LOCK — acquire mu.
//  3. CHECK — idempotency cache, game-over, turn order, seq match, clock.
//  4. MUTATE — debit clock, push move into chess.Game, apply increment,
//     update repetition + draw state, advance seq.
//  5. SNAPSHOT — copy a coherent BoardSnapshot value out under the lock.
//  6. UNLOCK.
//  7. RENDER + BROADCAST — encode signals JSON once via dsenc, publish to
//     hub. The same []byte goes to every subscriber.
//
// Returns the new ActionResult on success or a typed error. On any
// non-nil-error path the room state is unchanged.
//
// Errors:
//
//   - ErrBadMoveSyntax    — UCI regex failed (defensive; HTTP layer already
//     filters).
//   - ErrNotParticipant   — PlayerID isn't White or Black.
//   - ErrGameOver         — Outcome already set.
//   - ErrFlagged          — active side's clock reached zero at adjudication.
//   - ErrNotYourTurn      — req.PlayerID isn't the active side.
//   - ErrStaleSequence    — req.Seq != current seq.
//   - ErrIllegalMove      — chess engine rejected the move.
//
// Idempotent retry: if r.seenActions has the same ClientActionID, the
// cached ActionResult is returned, no mutation happens, and no broadcast
// is re-emitted (the original publish already went out).
func (r *GameRoom) ApplyMove(ctx context.Context, req MoveReq, now time.Time) (ActionResult, error) {
	// Defensive syntax check (cheap; HTTP layer should have done this).
	if err := ValidateUCISyntax(req.Move); err != nil {
		return ActionResult{}, err
	}

	r.mu.Lock()

	// Idempotency: same client_action_id → return cached result, do not
	// re-apply or re-broadcast. FR-013 / SC-006.
	if cached, ok := r.seenActions[req.ClientActionID]; ok {
		r.mu.Unlock()
		cached.Idempotent = true
		return cached, nil
	}

	// Authorization: only listed participants may move.
	color, ok := r.playerColor(req.PlayerID)
	if !ok {
		r.mu.Unlock()
		return ActionResult{}, ErrNotParticipant
	}

	// Already over? Reject before touching anything.
	if r.outcome != Ongoing {
		r.mu.Unlock()
		return ActionResult{}, ErrGameOver
	}

	// Sequence check: client must have observed our latest state.
	if req.Seq != r.seq {
		r.mu.Unlock()
		return ActionResult{}, ErrStaleSequence
	}

	// Turn check: req.PlayerID must be the active side. We compare colors,
	// not PlayerID, because we already resolved color above.
	if color != r.clocks.Active {
		r.mu.Unlock()
		return ActionResult{}, ErrNotYourTurn
	}

	// Debit the active clock at `now`. After this call, ActiveRemaining is
	// authoritative for the move-vs-flag decision below.
	r.clocks.Debit(now)
	if r.clocks.Remain[r.clocks.Active] <= 0 {
		// Adjudicate the flag right here. The active side loses; broadcast
		// the terminal as if a flag-timer had fired (FR-020 deterministic).
		r.adjudicateFlagLocked()
		r.seq++
		snap := r.snapshotLocked(now)
		bytes := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
		r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: bytes})
		r.mu.Unlock()
		r.hub.Publish(bytes)
		return ActionResult{}, ErrFlagged
	}

	// Push the move. Use the safe (validating) path on untrusted input.
	if err := r.game.PushNotationMove(req.Move, libchess.UCINotation{}, nil); err != nil {
		r.mu.Unlock()
		return ActionResult{}, ErrIllegalMove
	}

	// Move accepted: credit increment + switch sides.
	r.clocks.ApplyIncrementAndSwitch(now)

	// A move by the offering side withdraws the pending draw offer (FR-037
	// "implicitly decline").
	if r.drawOfferActive && r.drawOfferFrom == color {
		r.drawOfferActive = false
		r.drawOfferAtSeq = 0
	}

	// Update repetition + draw state. This also detects engine-side
	// terminals (checkmate, stalemate, insufficient material, fivefold,
	// 75-move) — see updateDrawStateLocked.
	r.updateDrawStateLocked(req.ClaimDraw)

	r.seq++

	snap := r.snapshotLocked(now)
	bytes := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
	r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: bytes})

	result := ActionResult{Seq: snap.Seq, Snapshot: snap}
	r.rememberAction(req.ClientActionID, result)

	r.mu.Unlock()

	// Broadcast outside the lock. Constitution V — same bytes for every
	// subscriber. Constitution III — never hold the room lock during I/O.
	r.hub.Publish(bytes)

	return result, nil
}

// adjudicateFlagLocked finalises the room as a loss-on-time for the active
// side. Caller must hold mu. Stops the flag timer if any.
func (r *GameRoom) adjudicateFlagLocked() {
	loser := r.clocks.Active
	if loser == White {
		r.outcome = BlackWin
	} else {
		r.outcome = WhiteWin
	}
	r.termination = FlagOnTime
	if r.flagTimer != nil {
		r.flagTimer.Stop()
		r.flagTimer = nil
	}
	// Clear any pending draw offer — terminal state nullifies it.
	r.drawOfferActive = false
}

// updateDrawStateLocked refreshes the repetition counter and detects every
// engine-driven termination condition. Run AFTER chess.Game accepts a move
// (or after a FEN load that puts the position into a terminal state).
//
// Strategy:
//
//  1. The corentings library does its own auto-detection of checkmate /
//     stalemate / insufficient material / fivefold / 75-move when a move
//     is pushed. If g.Outcome() != NoOutcome, mirror what the library
//     decided into our internal Outcome + TerminationReason — single source
//     of truth, no double-counting.
//
//  2. Claimable draws (threefold / 50-move) require the player to opt in
//     via req.ClaimDraw. We use our own repetition map for the threefold
//     check because we want to count from the GAME's first position,
//     including the starting position, which matches FIDE.
func (r *GameRoom) updateDrawStateLocked(claimDraw bool) {
	pos := r.game.Position()

	// Update our own repetition counter using the FIDE-correct hash from
	// the library (research R-10). The library's hash includes en-passant
	// only when an en-passant capture is actually legal.
	key := positionKey(pos)
	r.repetition[key]++

	// 1. Library-detected automatic terminal states.
	if r.game.Outcome() != libchess.NoOutcome {
		switch r.game.Method() {
		case libchess.Checkmate:
			// Mated side is the side-to-move; the OTHER side wins.
			if pos.Turn() == libchess.White {
				r.outcome = BlackWin
			} else {
				r.outcome = WhiteWin
			}
			r.termination = Checkmate
			return
		case libchess.Stalemate:
			r.outcome = Draw
			r.termination = Stalemate
			return
		case libchess.InsufficientMaterial:
			r.outcome = Draw
			r.termination = InsufficientMaterial
			return
		case libchess.FivefoldRepetition:
			r.outcome = Draw
			r.termination = FivefoldRepetition
			return
		case libchess.SeventyFiveMoveRule:
			r.outcome = Draw
			r.termination = SeventyFiveMoveRule
			return
		}
	}

	// 2. Position-Status() fallback — covers FEN-loaded terminals that
	// don't transit a Move() call. The library's engine.Status returns
	// Checkmate/Stalemate from a static position.
	switch pos.Status() {
	case libchess.Checkmate:
		if pos.Turn() == libchess.White {
			r.outcome = BlackWin
		} else {
			r.outcome = WhiteWin
		}
		r.termination = Checkmate
		return
	case libchess.Stalemate:
		r.outcome = Draw
		r.termination = Stalemate
		return
	}

	// 3. Claimable draws — only if the moving player asked for it via
	// ClaimDraw. Threefold requires the mover to claim ON or before the
	// move that produces the third occurrence.
	if claimDraw {
		if r.repetition[key] >= 3 {
			r.outcome = Draw
			r.termination = ThreefoldRepetition
			return
		}
		if pos.HalfMoveClock() >= 100 {
			r.outcome = Draw
			r.termination = FiftyMoveRule
			return
		}
	}
}
