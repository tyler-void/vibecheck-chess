package chess

// BoardSnapshot is the projected, transport-friendly view of a GameRoom's
// authoritative state. Returned by GET /games/{id}/snapshot as JSON and
// projected into Datastar signals on every accepted state transition (see
// contracts/sse-events.md).
//
// Field order matches data-model.md and is chosen for minimal struct padding
// on 64-bit Linux: int64s first, then strings, then small enums.
//
// All fields are values, not pointers — the snapshot is meant to be copied
// out of the room under the lock and serialised after the lock is released.
type BoardSnapshot struct {
	Seq                   uint64 `json:"seq"`
	ServerUnixMS          int64  `json:"server_unix_ms"`
	ActiveExpiresAtUnixMS int64  `json:"active_expires_at_unix_ms"`
	WhiteMS               int64  `json:"white_ms"`
	BlackMS               int64  `json:"black_ms"`
	LastMove              string `json:"last_move"`
	FEN                   string `json:"fen"`
	// DrawOfferFrom is "w", "b", or "" (no pending offer). The client surfaces
	// the "opponent offers a draw" affordance off this signal alone.
	DrawOfferFrom string            `json:"draw_offer_from"`
	Turn          Color             `json:"turn"`
	Outcome       Outcome           `json:"outcome"`
	Termination   TerminationReason `json:"termination"`
}
