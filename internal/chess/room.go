package chess

import (
	"sync"
	"time"

	libchess "github.com/corentings/chess/v2"

	"kisschess/internal/dsenc"
	"kisschess/internal/sse"
)

// ActionResult is the return value of every state-mutating apply path
// (ApplyMove, ApplyResign, ApplyDrawOffer, ApplyDrawAccept). Cached by
// client_action_id for idempotent retries (FR-013, SC-006). The same shape
// serves moves and actions per the data-model.md rev-2 rename.
//
// Idempotent is true when the caller hit the seenActions cache rather than
// applying a fresh mutation. The HTTP layer uses this to pick 204 (fresh)
// vs 200 (replayed) per contracts/http-api.md.
type ActionResult struct {
	Seq        uint64
	Snapshot   BoardSnapshot
	Idempotent bool
}

// SSEEvent is one entry in the per-room replay ring (FR-024). Holds the
// pre-rendered Datastar wire bytes so the broadcast hot path can write the
// same byte slice to every subscriber (Constitution V).
type SSEEvent struct {
	Seq   uint64
	Bytes []byte
}

// GameRoom is the authoritative in-process container for one game's state.
//
// Concurrency: mu protects ALL mutable fields below. All public mutation
// methods take the write lock; read-only snapshot methods take the read
// lock. mu is never held while writing to a network socket — the broadcast
// goes through hub.Publish which is itself non-blocking (Constitution III).
//
// Field ordering follows data-model.md (largest first) to minimise struct
// padding. Heap pointers, then maps, then ring + hub, then timer + clocks,
// then atomic-sized fields, then small enums and the bool, then the mutex.
type GameRoom struct {
	game        *libchess.Game
	seenActions map[string]ActionResult
	repetition  map[uint64]uint8
	recent      *Ring[SSEEvent]
	hub         *sse.FanoutHub
	flagTimer   *time.Timer
	clocks      ClockState
	id          GameID
	players     [2]PlayerID

	seq             uint64
	drawOfferAtSeq  uint64
	outcome         Outcome
	termination     TerminationReason
	drawOfferFrom   Color
	drawOfferActive bool

	mu sync.RWMutex
}

// NewGameRoom constructs a room ready to accept the first move. White is on
// the clock at t = now (matches the FIDE convention for online play: the
// clock starts at game-create time, not on a separate "start" signal).
// The flag timer is armed before return so White's initial time begins
// counting down immediately.
func NewGameRoom(id GameID, white, black PlayerID, tc TimeControl, now time.Time) *GameRoom {
	r := &GameRoom{
		game:        libchess.NewGame(),
		seenActions: make(map[string]ActionResult, 64),
		repetition:  make(map[uint64]uint8, 64),
		recent:      NewRing[SSEEvent](128),
		hub:         sse.NewFanoutHub(),
		clocks:      NewClockState(tc, now),
		id:          id,
		players:     [2]PlayerID{white, black},
	}
	// Seed repetition with the starting position so the first repeat is
	// counted from move 0.
	r.repetition[positionKey(r.game.Position())] = 1

	// Arm the initial flag timer. The room isn't shared with any other
	// goroutine yet so we don't strictly need the lock, but
	// resetFlagTimerLocked's contract says "caller holds mu" — take it
	// for the ~1µs critical section to keep the discipline mechanical.
	r.mu.Lock()
	r.resetFlagTimerLocked(now)
	r.mu.Unlock()

	return r
}

// ID returns the room's GameID. Lock-free; the field is set at construction
// and never mutated.
func (r *GameRoom) ID() GameID { return r.id }

// Participants returns the white and black player IDs. Lock-free; players
// are set at construction and never mutated. Used by the HTTP layer to
// render share-with-opponent links into the game page.
func (r *GameRoom) Participants() (white, black PlayerID) {
	return r.players[White], r.players[Black]
}

// Hub exposes the fan-out hub so the SSE handler can register subscribers.
// Lock-free; the field is set at construction and never re-assigned.
func (r *GameRoom) Hub() *sse.FanoutHub { return r.hub }

// Snapshot returns a defensive copy of the current authoritative state.
// Takes the read lock — safe to call concurrently with other readers.
// Stamps server_unix_ms / active_expires_at_unix_ms / white_ms / black_ms
// against the supplied `now` so a single call yields a coherent view.
func (r *GameRoom) Snapshot(now time.Time) BoardSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked(now)
}

// snapshotLocked is the lock-held flavour. Callers must hold mu (read or
// write). The HTTP and SSE paths invoke this from within ApplyMove etc.
// after the mutation so the returned snapshot is what we publish.
func (r *GameRoom) snapshotLocked(now time.Time) BoardSnapshot {
	whiteRem, blackRem := r.clocks.Remain[White], r.clocks.Remain[Black]
	// Project the active side's remaining time AT now without mutating the
	// clock (ActiveRemaining is read-only).
	switch r.clocks.Active {
	case White:
		whiteRem = r.clocks.ActiveRemaining(now)
	case Black:
		blackRem = r.clocks.ActiveRemaining(now)
	}
	expires := now.Add(r.clocks.ActiveRemaining(now)).UnixMilli()
	if r.outcome != Ongoing {
		expires = 0
	}

	lastMove := ""
	if ms := r.game.Moves(); len(ms) > 0 {
		lastMove = libchess.UCINotation{}.Encode(nil, ms[len(ms)-1])
	}

	drawFrom := ""
	if r.drawOfferActive {
		drawFrom = r.drawOfferFrom.String()
	}

	// Enumerate the side-to-move's legal moves so the client can refuse
	// illegal drags before they animate (Option B — server is the authority,
	// no engine on the client). Skipped when the game is over: no moves to
	// play, and we keep the wire payload tiny.
	var legal []string
	if r.outcome == Ongoing {
		moves := r.game.Position().ValidMoves()
		legal = make([]string, len(moves))
		enc := libchess.UCINotation{}
		for i := range moves {
			legal[i] = enc.Encode(nil, &moves[i])
		}
	}

	// check-detection: the side-to-move is in check iff the LAST move played
	// carries the Check tag. The corentings engine sets this tag on every
	// legal move whose application leaves the OPPONENT in check — i.e. the
	// position now in front of us.
	checkSq := ""
	if r.outcome == Ongoing {
		if ms := r.game.Moves(); len(ms) > 0 {
			last := ms[len(ms)-1]
			if last.HasTag(libchess.Check) {
				checkSq = findKingSquare(r.game.Position(), r.clocks.Active)
			}
		}
	}

	// claimable draws — surface BEFORE we apply auto-detection so the client
	// can let the player decide. Threefold uses our internal counter (FIDE-
	// correct hash includes en-passant). Fifty-move uses the position's
	// halfmove clock.
	canClaim := false
	if r.outcome == Ongoing {
		pos := r.game.Position()
		if r.repetition[positionKey(pos)] >= 3 || pos.HalfMoveClock() >= 100 {
			canClaim = true
		}
	}

	return BoardSnapshot{
		Seq:                   r.seq,
		ServerUnixMS:          now.UnixMilli(),
		ActiveExpiresAtUnixMS: expires,
		WhiteMS:               whiteRem.Milliseconds(),
		BlackMS:               blackRem.Milliseconds(),
		LastMove:              lastMove,
		FEN:                   r.game.FEN(),
		DrawOfferFrom:         drawFrom,
		Turn:                  r.clocks.Active,
		Outcome:               r.outcome,
		Termination:           r.termination,
		LegalMoves:            legal,
		CheckSquare:           checkSq,
		CanClaimDraw:          canClaim,
	}
}

// findKingSquare scans the board for the king of the given color and returns
// its square in algebraic ("e1", "g8") notation, or "" if no king is on the
// board (defensive — should never happen in a legal game).
func findKingSquare(pos *libchess.Position, c Color) string {
	want := libchess.WhiteKing
	if c == Black {
		want = libchess.BlackKing
	}
	for sq, p := range pos.Board().SquareMap() {
		if p == want {
			return sq.String()
		}
	}
	return ""
}

// recentAfterLocked returns SSE wire-byte payloads from the replay ring with
// seq strictly greater than lastSeq, or reports a gap so the handler can
// fall back to a fresh hydration (FR-025). Caller MUST hold mu (read or
// write).
func (r *GameRoom) recentAfterLocked(lastSeq uint64) (events []SSEEvent, ok bool) {
	return r.recent.AfterSeq(lastSeq)
}

// HydrateBytes returns a fresh datastar-patch-signals payload reflecting the
// room's CURRENT state. Used by the SSE handler for brand-new clients that
// connect without a Last-Event-ID header — those clients need the
// authoritative snapshot regardless of whether r.seq happens to equal 0.
func (r *GameRoom) HydrateBytes(now time.Time) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := r.snapshotLocked(now)
	return dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
}

// ReplayOrSnapshot is the public reconnect helper used by the SSE handler.
// Returns either a slice of SSE byte payloads to replay (true) or a single
// fresh-hydration payload alongside `false` so the handler knows to skip
// straight to live broadcast after writing it. The contract matches the
// "replay or snapshot" rule from contracts/sse-events.md.
func (r *GameRoom) ReplayOrSnapshot(lastSeq uint64, now time.Time) (events [][]byte, snapshotPayload []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if lastSeq == r.seq {
		// Client is up to date.
		return nil, nil
	}
	if ev, ok := r.recent.AfterSeq(lastSeq); ok && len(ev) > 0 {
		out := make([][]byte, 0, len(ev))
		for _, e := range ev {
			out = append(out, e.Bytes)
		}
		return out, nil
	}
	// Gap too large — fresh hydration.
	snap := r.snapshotLocked(now)
	return nil, dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
}

// playerColor returns the Color of the player with id p, or false if p isn't
// a participant. Lock-free; r.players is set at construction.
func (r *GameRoom) playerColor(p PlayerID) (Color, bool) {
	switch p {
	case r.players[White]:
		return White, true
	case r.players[Black]:
		return Black, true
	default:
		return 0, false
	}
}

// activePlayerLocked returns the id of the player whose turn it currently
// is. Caller must hold mu.
func (r *GameRoom) activePlayerLocked() PlayerID {
	return r.players[r.clocks.Active]
}

// rememberAction caches a successful ActionResult under the client-supplied
// idempotency key. The cache is bounded — when it exceeds 64 entries the
// oldest-by-iteration entry is evicted. Map iteration order is randomised
// in Go, which is the cheapest LRU approximation we get for free.
//
// 64 is comfortably larger than any realistic in-flight retry burst (TCP
// retransmits, brief network blips) for one game.
func (r *GameRoom) rememberAction(key string, result ActionResult) {
	if key == "" {
		return // tests / internal callers may omit; HTTP layer always supplies.
	}
	if len(r.seenActions) >= 64 {
		for k := range r.seenActions {
			delete(r.seenActions, k)
			break
		}
	}
	r.seenActions[key] = result
}
