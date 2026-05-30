// Package chess holds the authoritative in-process state for one or more
// chess games: rooms, the manager registry, clocks, the move-validation glue
// around corentings/chess/v2, the bounded replay ring, and the typed errors.
//
// Constitution gates (.specify/memory/constitution.md):
//
//   - II  in-memory authoritative state — every active game has exactly one
//     in-process owner.
//   - III lock-order discipline (NON-NEGOTIABLE) — parse outside the lock,
//     mutate inside, unlock before any network I/O.
//   - V   render once, broadcast same bytes — the broadcast hot path uses
//     internal/dsenc, not the Datastar SDK.
package chess

// GameID is the opaque, externally-assigned identifier for a single game
// (typically a ULID). Two games with the same id MUST refer to the same
// authoritative state — the GameManager enforces single-owner per id.
type GameID string

// PlayerID is the opaque identity of a single human (or bot) participant.
// Authorisation is upstream of this package; we trust the value provided.
type PlayerID string

// ClientID identifies one connected SSE subscriber for one game. Assigned by
// the FanoutHub via an atomic counter.
type ClientID uint64

// Color is the side a player plays. White is 0, Black is 1 — chosen so the
// value can index a [2]T array directly without a switch.
type Color uint8

const (
	White Color = iota
	Black
)

func (c Color) String() string {
	switch c {
	case White:
		return "w"
	case Black:
		return "b"
	default:
		return "?"
	}
}

// opposite returns the other side. Defined as a package-private helper because
// turn-flip happens only inside lock-protected mutations.
func opposite(c Color) Color {
	return c ^ 1
}

// Outcome captures whether a game has terminated and, if so, who won.
//
// Ongoing → {WhiteWin, BlackWin, Draw} is the only legal transition; once a
// terminal value is set the room rejects further moves (FR-009).
type Outcome uint8

const (
	Ongoing Outcome = iota
	WhiteWin
	BlackWin
	Draw
)

// TerminationReason explains why the game ended. Mirrors data-model.md.
// NoTermination is the zero value and applies while Outcome == Ongoing.
type TerminationReason uint8

const (
	NoTermination TerminationReason = iota
	Checkmate
	Stalemate
	InsufficientMaterial
	FivefoldRepetition
	SeventyFiveMoveRule
	ThreefoldRepetition // claimed
	FiftyMoveRule       // claimed
	FlagOnTime
	Resignation
	AgreedDraw
)
