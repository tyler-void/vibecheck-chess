package dsenc

import (
	"testing"
)

// Phase 8 / T082 — steady-state allocation budget on the broadcast hot
// path. Constitution V (Render Once, Broadcast Same Bytes) and FR-031
// (bounded memory growth) make this gate load-bearing: at 10k CCU with a
// typical move cadence we encode thousands of frames per second; an
// allocation budget overrun shows up as GC pressure, which compounds with
// the bounded-queue dynamics of Constitution IV.
//
// The targets:
//
//   - 1 alloc/op (the returned []byte itself — by definition the caller
//     owns it; we cannot pool it).
//   - < 1000 ns/op on commodity hardware. The hot path is a handful of
//     byte appends and a memcpy — the only meaningful cost.
//
// These targets are validated at b.N >> 1 so the sync.Pool warmup amortises
// across iterations.

// Representative signal payload — a real chess SSE frame.
var benchSignalsPayload = []byte(`{"seq":42,"server_unix_ms":1780148130012,"active_expires_at_unix_ms":1780148290012,"white_ms":160000,"black_ms":158000,"turn":"w","outcome":0,"termination":0,"last_move":"e7e5","fen":"rnbqkbnr/pppp1ppp/8/4p3/4P3/8/PPPP1PPP/RNBQKBNR w KQkq e6 0 2","draw_offer_from":""}`)

// Allocation check — assert 1 alloc/op in steady state. The benchmark
// itself is not a test, but the test below uses testing.Benchmark to
// invoke it inline and assert the alloc count.
func BenchmarkPatchSignalsSteady(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PatchSignalsBytes(uint64(i), benchSignalsPayload)
	}
}

func BenchmarkPatchElementsSteady(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	selector := "#move-list"
	mode := "append"
	elements := `<li data-seq="42">e4</li>`
	for i := 0; i < b.N; i++ {
		_ = PatchElementsBytes(uint64(i), selector, mode, elements)
	}
}

// TestAllocBudget asserts the steady-state allocation count against the
// constitutional gate. testing.Benchmark runs the bench with whatever
// b.N the runtime picks (typically 100k+ iterations) and returns the
// observed AllocsPerOp. Anything above 2 means the sync.Pool isn't
// reusing the working buffer.
//
// We allow up to 2 allocs/op as a conservative upper bound: the returned
// []byte (1 alloc, unavoidable) + possibly one for the initial
// strconv.AppendUint scratch on cold paths. In practice steady state is
// 1, but the tolerance prevents flakes on CI under -race (the race
// detector instruments allocations).
func TestAllocBudget(t *testing.T) {
	res := testing.Benchmark(BenchmarkPatchSignalsSteady)
	if res.N == 0 {
		t.Skip("benchmark did not run")
	}
	if allocs := res.AllocsPerOp(); allocs > 2 {
		t.Errorf("PatchSignalsBytes allocs/op = %d, want ≤ 2 (steady state target is 1)", allocs)
	}
	// Latency ceiling — generous to absorb CI noise. The real bound is
	// the broadcast-hot-path budget at 10k CCU; locally we just want a
	// flag if a regression takes the encoder above 10x its ceiling.
	if ns := res.NsPerOp(); ns > 10_000 {
		t.Errorf("PatchSignalsBytes ns/op = %d, want ≤ 10000 (target is < 1000)", ns)
	}
}
