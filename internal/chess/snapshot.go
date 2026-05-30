package chess

import (
	"strconv"
	"sync"
)

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

// signalsBufPool reuses []byte scratch buffers for toSignalsJSON. Sized to
// fit a typical snapshot (~250 bytes) without grow.
var signalsBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 384)
		return &b
	},
}

// toSignalsJSON encodes the snapshot as a single line of JSON suitable for
// the `data: signals ...` line of a `datastar-patch-signals` SSE frame. The
// output is keyed identically to the JSON tags above but written by hand to
// guarantee:
//
//   - Stable field order (encoding/json randomises map iteration order for
//     map fields, but a struct is fine; we hand-write anyway so the golden
//     wire test stays deterministic regardless of stdlib changes).
//   - Single line (no embedded newlines, required by the Datastar wire format).
//   - One heap allocation per call (the returned []byte). The intermediate
//     scratch buffer is pool-reused.
//
// Returns a freshly allocated, caller-owned byte slice.
func (s BoardSnapshot) toSignalsJSON() []byte {
	bp := signalsBufPool.Get().(*[]byte)
	b := (*bp)[:0]
	defer func() {
		*bp = b
		signalsBufPool.Put(bp)
	}()

	b = append(b, '{')
	b = appendKVUint(b, "seq", s.Seq, false)
	b = appendKVInt(b, "server_unix_ms", s.ServerUnixMS, true)
	b = appendKVInt(b, "active_expires_at_unix_ms", s.ActiveExpiresAtUnixMS, true)
	b = appendKVInt(b, "white_ms", s.WhiteMS, true)
	b = appendKVInt(b, "black_ms", s.BlackMS, true)
	b = appendKVString(b, "last_move", s.LastMove, true)
	b = appendKVString(b, "fen", s.FEN, true)
	b = appendKVString(b, "draw_offer_from", s.DrawOfferFrom, true)
	b = appendKVString(b, "turn", s.Turn.String(), true)
	b = appendKVInt(b, "outcome", int64(s.Outcome), true)
	b = appendKVInt(b, "termination", int64(s.Termination), true)
	b = append(b, '}')

	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func appendKVUint(b []byte, k string, v uint64, comma bool) []byte {
	if comma {
		b = append(b, ',')
	}
	b = append(b, '"')
	b = append(b, k...)
	b = append(b, '"', ':')
	return strconv.AppendUint(b, v, 10)
}

func appendKVInt(b []byte, k string, v int64, comma bool) []byte {
	if comma {
		b = append(b, ',')
	}
	b = append(b, '"')
	b = append(b, k...)
	b = append(b, '"', ':')
	return strconv.AppendInt(b, v, 10)
}

// appendKVString writes a key:"value" pair, doing minimal JSON-escape on
// value. Our snapshot strings are FEN, UCI moves, and a 1-char color tag —
// none contain quotes or backslashes — but we escape defensively so a
// future field addition can't silently produce invalid JSON.
func appendKVString(b []byte, k, v string, comma bool) []byte {
	if comma {
		b = append(b, ',')
	}
	b = append(b, '"')
	b = append(b, k...)
	b = append(b, '"', ':', '"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0',
					hexDigit(c>>4), hexDigit(c&0xf))
			} else {
				b = append(b, c)
			}
		}
	}
	b = append(b, '"')
	return b
}

func hexDigit(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' + n - 10
}
