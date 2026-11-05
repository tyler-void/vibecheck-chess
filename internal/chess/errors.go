package chess

import "errors"

// Typed errors returned by the chess package. Each is mapped to a stable
// machine-readable code + HTTP status in internal/http/games.go per
// specs/001-kiss-chess-engine/contracts/http-api.md.
//
// All errors are sentinel values comparable with errors.Is.
var (
	// ErrGameNotFound — the requested GameID is not registered with the
	// GameManager. FR-002. Maps to HTTP 404.
	ErrGameNotFound = errors.New("chess: game not found")

	// ErrBadMoveSyntax — the move string does not match the UCI regex
	// `^[a-h][1-8][a-h][1-8][qrbn]?$`. FR-005. Maps to HTTP 400.
	ErrBadMoveSyntax = errors.New("chess: bad move syntax")

	// ErrNotYourTurn — the submitting player is not the active player on
	// the room's current Color. FR-006. Maps to HTTP 409.
	ErrNotYourTurn = errors.New("chess: not your turn")

	// ErrStaleSequence — the request's seq does not match the room's
	// current seq. FR-012. The HTTP layer attaches current_snapshot for
	// reconciliation. Maps to HTTP 409.
	ErrStaleSequence = errors.New("chess: stale sequence")

	// ErrIllegalMove — corentings/chess/v2 rejected the move as illegal in
	// the current position. FR-007. Maps to HTTP 422.
	ErrIllegalMove = errors.New("chess: illegal move")

	// ErrGameOver — Outcome has already been set; no further moves or
	// actions are accepted. FR-009. Maps to HTTP 409.
	ErrGameOver = errors.New("chess: game over")

	// ErrFlagged — the active player's clock reached zero at adjudication
	// time. FR-018. Maps to HTTP 409.
	ErrFlagged = errors.New("chess: flagged on time")

	// ErrNotParticipant — the submitter (X-Player-ID) is neither White nor
	// Black for the room. FR-036 / FR-037. Maps to HTTP 409.
	ErrNotParticipant = errors.New("chess: not a participant of this game")

	// ErrNoPendingOffer — accept arrived but no draw offer is pending.
	// FR-037. Maps to HTTP 409.
	ErrNoPendingOffer = errors.New("chess: no pending draw offer")

	// ErrOwnOffer — submitter tried to accept their own pending offer.
	// FR-037. Maps to HTTP 409.
	ErrOwnOffer = errors.New("chess: cannot accept your own draw offer")

	// ErrNoClaimableDraw — claim-draw submitted but neither threefold
	// repetition nor the fifty-move rule applies to the current position.
	// Maps to HTTP 409.
	ErrNoClaimableDraw = errors.New("chess: no claimable draw at this position")
)
