package dsenc

import (
	"strings"
	"testing"
)

// Datastar v1.0+ wire format reference: https://data-star.dev/reference/sse_events
//
// Exactly two event types are emitted on the broadcast hot path:
//
//   event: datastar-patch-signals
//   id: <seq>
//   data: signals <json-on-one-line>
//   ⏎
//
//   event: datastar-patch-elements
//   id: <seq>
//   data: selector <css-selector>
//   data: mode <outer|inner|replace|prepend|append|before|after|remove>
//   data: elements <html-line>
//   ⏎
//
// Note: SSE event terminator is a single blank line, encoded as "\n\n" after
// the last data: line. The wire bytes therefore end with "\n\n".

func TestPatchSignalsBytes_GoldenMinimal(t *testing.T) {
	got := string(PatchSignalsBytes(42, []byte(`{"white_ms":160000,"black_ms":158000}`)))
	want := "event: datastar-patch-signals\nid: 42\ndata: signals {\"white_ms\":160000,\"black_ms\":158000}\n\n"
	if got != want {
		t.Errorf("PatchSignalsBytes mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Zero seq is a legitimate value (initial hydration may carry id: 0).
func TestPatchSignalsBytes_ZeroSeq(t *testing.T) {
	got := string(PatchSignalsBytes(0, []byte(`{}`)))
	want := "event: datastar-patch-signals\nid: 0\ndata: signals {}\n\n"
	if got != want {
		t.Errorf("zero-seq signals mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestPatchElementsBytes_GoldenAppend(t *testing.T) {
	got := string(PatchElementsBytes(43, "#move-list", "append", `<li data-seq="43">e4</li>`))
	want := "event: datastar-patch-elements\nid: 43\ndata: selector #move-list\ndata: mode append\ndata: elements <li data-seq=\"43\">e4</li>\n\n"
	if got != want {
		t.Errorf("PatchElementsBytes mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestPatchElementsBytes_GoldenOuter(t *testing.T) {
	got := string(PatchElementsBytes(1, "#game-root", "outer",
		`<main id="game-root"><div id="board"></div></main>`))
	want := "event: datastar-patch-elements\nid: 1\ndata: selector #game-root\ndata: mode outer\ndata: elements <main id=\"game-root\"><div id=\"board\"></div></main>\n\n"
	if got != want {
		t.Errorf("PatchElementsBytes outer mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Every emitted frame ends with the SSE event terminator "\n\n" — without it
// browsers buffer the event indefinitely.
func TestEvents_AlwaysEndWithBlankLine(t *testing.T) {
	signals := string(PatchSignalsBytes(1, []byte(`{}`)))
	if !strings.HasSuffix(signals, "\n\n") {
		t.Errorf("signals frame missing terminator; tail: %q", signals[len(signals)-4:])
	}
	elements := string(PatchElementsBytes(1, "#x", "outer", "<p/>"))
	if !strings.HasSuffix(elements, "\n\n") {
		t.Errorf("elements frame missing terminator; tail: %q", elements[len(elements)-4:])
	}
}

// Constitution V — render once, broadcast same bytes. Calling the encoder
// twice with the same inputs MUST produce byte-identical output (no
// per-call timestamps, no randomness).
func TestEncode_Deterministic(t *testing.T) {
	a := PatchSignalsBytes(7, []byte(`{"x":1}`))
	b := PatchSignalsBytes(7, []byte(`{"x":1}`))
	if string(a) != string(b) {
		t.Errorf("encoder non-deterministic\n a: %q\n b: %q", a, b)
	}
}

// Output is heap-allocated per call (no shared buffer escape). Test that the
// caller can mutate the returned slice without poisoning a sync.Pool entry.
func TestEncode_ReturnedSliceIsOwnedByCaller(t *testing.T) {
	a := PatchSignalsBytes(1, []byte(`{}`))
	original := string(a)
	// Caller writes into the returned slice — must not affect a future call.
	for i := range a {
		a[i] = 'X'
	}
	b := PatchSignalsBytes(1, []byte(`{}`))
	if string(b) != original {
		t.Errorf("second call returned tainted bytes; got %q want %q", b, original)
	}
}

// Allocation budget: each frame should fit on the broadcast hot path without
// triggering more than one heap allocation (the returned []byte itself).
// sync.Pool reuse for the working buffer SHOULD bring this to 1 alloc/op.
func BenchmarkPatchSignals(b *testing.B) {
	payload := []byte(`{"seq":42,"server_unix_ms":1780148130012,"white_ms":160000,"black_ms":158000,"turn":"w","fen":"rnbqkbnr/pppp1ppp/8/4p3/4P3/8/PPPP1PPP/RNBQKBNR w KQkq e6 0 2"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PatchSignalsBytes(uint64(i), payload)
	}
}
