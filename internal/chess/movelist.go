package chess

import (
	"strconv"

	"kisschess/internal/dsenc"
)

// buildMoveListAppend produces the SSE bytes for a single
// `datastar-patch-elements` event that appends one <li> to #move-list. The
// element carries `data-seq="<seq>"` so a client that already has that
// element (e.g. after a Last-Event-ID replay duplicates an entry) can
// dedup or skip rendering.
//
// The move text is the UCI string — SAN conversion is a Phase 9 polish
// nice-to-have that we deliberately did NOT pull in (would need the
// position BEFORE the move; we already have it but the encoding adds
// ~50 LOC for a cosmetic improvement).
func buildMoveListAppend(seq uint64, uciMove string) []byte {
	// <li data-seq="42" class="move">e2e4</li>
	const opener = `<li data-seq="`
	const middle = `" class="move">`
	const closer = `</li>`

	// Pre-size the buffer: opener + seq + middle + uciMove + closer.
	bufLen := len(opener) + 20 + len(middle) + len(uciMove) + len(closer)
	li := make([]byte, 0, bufLen)
	li = append(li, opener...)
	li = strconv.AppendUint(li, seq, 10)
	li = append(li, middle...)
	li = append(li, uciMove...)
	li = append(li, closer...)

	return dsenc.PatchElementsBytes(seq, "#move-list", "append", string(li))
}
