// Package dsenc encodes Datastar v1.0+ SSE wire frames for the broadcast
// hot path.
//
// The Datastar Go SDK (github.com/starfederation/datastar-go/datastar) ties
// encoding to a single http.ResponseWriter via NewSSE(w, r) and exposes no
// public Marshal/Bytes helper. At 10k+ CCU with 1k spectators on a hot game,
// a naive SDK call per subscriber would force N marshals + N writes per
// accepted action. The hub broadcasts the same []byte to every subscriber
// (Constitution V — "render once, broadcast same bytes"), so we encode once
// here and let the SDK keep its scope to unicast paths (initial hydration,
// error responses).
//
// The wire spec is small and stable as of Datastar v1.0+:
// https://data-star.dev/reference/sse_events
//
//	event: datastar-patch-signals
//	id: <seq>
//	data: signals <one-line-json>
//	⏎
//
//	event: datastar-patch-elements
//	id: <seq>
//	data: selector <css-selector>
//	data: mode <outer|inner|replace|prepend|append|before|after|remove>
//	data: elements <one-line-html>
//	⏎
//
// If a future Datastar SDK release exposes a public Bytes() / MarshalSSE()
// helper, this package can be deleted (Complexity Tracking entry in plan.md).
package dsenc

import (
	"bytes"
	"strconv"
	"sync"
)

// bufPool reuses scratch bytes.Buffers to keep the broadcast hot path
// allocation-free except for the returned []byte itself.
var bufPool = sync.Pool{
	New: func() any {
		// Pre-sized to fit a typical signals frame (~300 bytes) without grow.
		return bytes.NewBuffer(make([]byte, 0, 512))
	},
}

// PatchSignalsBytes builds a `datastar-patch-signals` SSE frame for one
// broadcast. signalsJSON must be a single line of JSON (no embedded newlines
// — Datastar's `data:` semantics are line-oriented). Returns a freshly
// allocated byte slice owned by the caller.
//
// The returned slice MUST be safe for the caller to retain or mutate; the
// shared buffer is released back to the pool before return.
func PatchSignalsBytes(seq uint64, signalsJSON []byte) []byte {
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	defer bufPool.Put(b)

	b.WriteString("event: datastar-patch-signals\n")
	b.WriteString("id: ")
	b.Write(strconv.AppendUint(b.AvailableBuffer(), seq, 10))
	b.WriteByte('\n')
	b.WriteString("data: signals ")
	b.Write(signalsJSON)
	b.WriteString("\n\n")

	out := make([]byte, b.Len())
	copy(out, b.Bytes())
	return out
}

// PatchElementsBytes builds a `datastar-patch-elements` SSE frame. selector,
// mode, and elements must each fit on a single line (no embedded newlines).
//
// Mode is the merge strategy per the Datastar reference:
// outer | inner | replace | prepend | append | before | after | remove.
//
// Returns a freshly allocated byte slice owned by the caller.
func PatchElementsBytes(seq uint64, selector, mode, elements string) []byte {
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	defer bufPool.Put(b)

	b.WriteString("event: datastar-patch-elements\n")
	b.WriteString("id: ")
	b.Write(strconv.AppendUint(b.AvailableBuffer(), seq, 10))
	b.WriteByte('\n')
	b.WriteString("data: selector ")
	b.WriteString(selector)
	b.WriteByte('\n')
	b.WriteString("data: mode ")
	b.WriteString(mode)
	b.WriteByte('\n')
	b.WriteString("data: elements ")
	b.WriteString(elements)
	b.WriteString("\n\n")

	out := make([]byte, b.Len())
	copy(out, b.Bytes())
	return out
}
