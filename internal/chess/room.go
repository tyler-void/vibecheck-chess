package chess

import "sync"

// GameRoom is the authoritative in-process state container for one game.
// This file holds the STUB declaration so GameManager (manager.go) and the
// HTTP layer can compile in Phase 2; the real fields, ApplyMove,
// ApplyResign, ApplyDrawOffer / Accept, the flag timer, and the repetition
// map land in Phase 3 (tasks T037, T038, T043, T101, T102, T103).
//
// Keeping this stub minimal — just the room mutex and id — lets Phase 2
// foundational tests + the HTTP server skeleton compile without forward
// references. Production fields per data-model.md are documented inline.
type GameRoom struct {
	// mu protects all mutable fields below. Lock-order discipline
	// (Constitution III): held ONLY for state mutation; never held while
	// writing to a socket.
	mu sync.RWMutex

	// id is set at construction and immutable. Public via ID() so tests
	// and the HTTP layer can read it without locking.
	id GameID

	// Phase 3 will populate these fields; declared here for the data-model
	// field-order discipline (data-model.md):
	//   game            *chess.Game
	//   seenActions     map[string]ActionResult
	//   repetition      map[uint64]uint8
	//   recent          Ring[SSEEvent]
	//   hub             *sse.FanoutHub
	//   flagTimer       *time.Timer
	//   clocks          ClockState
	//   players         [2]PlayerID
	//   seq             uint64
	//   drawOfferAtSeq  uint64
	//   outcome         Outcome
	//   termination     TerminationReason
	//   drawOfferFrom   Color
	//   drawOfferActive bool
}

// ID returns the room's GameID. Safe to call without holding the lock.
func (r *GameRoom) ID() GameID { return r.id }

// newRoomStub builds a minimal room sufficient for Phase 2 manager tests.
// Phase 3's NewGameRoom(id, white, black, tc) replaces this with the full
// constructor (T038).
func newRoomStub(id GameID) *GameRoom {
	return &GameRoom{id: id}
}
