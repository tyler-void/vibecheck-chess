package chess

import (
	"encoding/binary"

	libchess "github.com/corentings/chess/v2"
)

// positionKey returns a 64-bit fingerprint of the current position that is
// suitable for repetition counting (research R-10, FR-010).
//
// Implementation: the corentings library exposes Position.Hash() which
// returns a 16-byte digest. The library's hash incorporates piece placement,
// side-to-move, castling rights, and en-passant only when an en-passant
// capture is actually legal — the FIDE-correct definition for repetition
// (Module C of the reference doc).
//
// We compress 16 bytes → uint64 by taking the first 8 bytes; the collision
// probability across a single game's positions is negligible (a typical
// game is well under 1,000 distinct positions; 2^64 keyspace).
func positionKey(p *libchess.Position) uint64 {
	h := p.Hash()
	return binary.BigEndian.Uint64(h[:8])
}
