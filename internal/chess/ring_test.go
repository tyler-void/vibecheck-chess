package chess

import "testing"

// FR-024 bounded ring buffer · FR-025 replay-or-snapshot.

func TestRing_EmptyHasNoGap(t *testing.T) {
	r := NewRing[int](8)
	got, ok := r.AfterSeq(42)
	if !ok || got != nil {
		t.Errorf("empty ring AfterSeq(42) = (%v, %v); want (nil, true)", got, ok)
	}
}

func TestRing_AppendAndReplay(t *testing.T) {
	r := NewRing[int](8)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Append(seq, int(seq*100))
	}

	got, ok := r.AfterSeq(2)
	if !ok {
		t.Fatalf("AfterSeq(2) reported gap; want full replay")
	}
	want := []int{300, 400, 500}
	if len(got) != len(want) {
		t.Fatalf("AfterSeq(2) len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRing_UpToDateNoReplay(t *testing.T) {
	r := NewRing[int](8)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Append(seq, int(seq))
	}
	got, ok := r.AfterSeq(5)
	if !ok || got != nil {
		t.Errorf("AfterSeq(latest) = (%v, %v); want (nil, true)", got, ok)
	}
}

// Capacity 4, push 10 entries — oldest 6 are evicted; reading from a stale
// pre-eviction seq must report a gap.
func TestRing_GapReportedWhenLastSeqTooOld(t *testing.T) {
	r := NewRing[int](4)
	for seq := uint64(1); seq <= 10; seq++ {
		r.Append(seq, int(seq))
	}
	// Ring now holds seqs 7..10.
	got, ok := r.AfterSeq(2)
	if ok {
		t.Errorf("AfterSeq(2) on evicted ring reported ok=true (got=%v); want gap (ok=false)", got)
	}
}

// Boundary: lastSeq is exactly one before the oldest stored — that's a clean
// replay, not a gap.
func TestRing_BoundaryReplayAtOldestMinusOne(t *testing.T) {
	r := NewRing[int](4)
	for seq := uint64(1); seq <= 10; seq++ {
		r.Append(seq, int(seq))
	}
	// Ring holds 7..10. lastSeq=6 → want 7,8,9,10.
	got, ok := r.AfterSeq(6)
	if !ok {
		t.Fatalf("AfterSeq(oldest-1) reported gap; want clean replay")
	}
	want := []int{7, 8, 9, 10}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
}

// Cap and Len reflect overflow behaviour.
func TestRing_LenSaturates(t *testing.T) {
	r := NewRing[int](3)
	r.Append(1, 1)
	r.Append(2, 2)
	if r.Len() != 2 {
		t.Errorf("Len after 2 appends = %d, want 2", r.Len())
	}
	r.Append(3, 3)
	r.Append(4, 4)
	if r.Len() != 3 || r.Cap() != 3 {
		t.Errorf("after overflow Len=%d Cap=%d, want 3/3", r.Len(), r.Cap())
	}
}
