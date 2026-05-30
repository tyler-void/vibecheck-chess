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
// Text content is SAN (Standard Algebraic Notation): "e4", "Nf3", "O-O",
// "Qxd5+", "exf8=Q#". Computed by the caller against the position BEFORE
// the move was played and passed in pre-rendered to keep this builder
// allocation-cheap.
func buildMoveListAppend(seq uint64, san string) []byte {
	// <li data-seq="42" class="move">Nf3</li>
	const opener = `<li data-seq="`
	const middle = `" class="move">`
	const closer = `</li>`

	bufLen := len(opener) + 20 + len(middle) + len(san) + len(closer)
	li := make([]byte, 0, bufLen)
	li = append(li, opener...)
	li = strconv.AppendUint(li, seq, 10)
	li = append(li, middle...)
	li = append(li, san...)
	li = append(li, closer...)

	return dsenc.PatchElementsBytes(seq, "#move-list", "append", string(li))
}
