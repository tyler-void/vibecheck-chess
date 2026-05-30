package chess

// Ring is a fixed-capacity replay buffer used per-GameRoom to hold the most
// recent SSE events for Last-Event-ID reconnects (FR-024 / FR-025 / research
// R-8). When a viewer reconnects with `Last-Event-ID: N`, the room asks the
// ring for everything after seq N; if the gap is too large, the ring reports
// a miss and the handler falls back to a fresh hydration.
//
// Capacity is fixed at construction. Append overwrites the oldest entry on
// overflow. The ring is NOT safe for concurrent access — callers (always the
// GameRoom) MUST hold the room's lock.
type Ring[T any] struct {
	items []T      // backing array of length cap
	seqs  []uint64 // seq[i] is the sequence number tagged on items[i] (0 means slot empty)
	head  int      // index of the NEXT write position
	size  int      // number of valid entries, <= cap(items)
}

// NewRing constructs a Ring with the given capacity. cap must be > 0.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring[T]{
		items: make([]T, capacity),
		seqs:  make([]uint64, capacity),
	}
}

// Cap returns the configured capacity.
func (r *Ring[T]) Cap() int { return cap(r.items) }

// Len returns the number of valid entries currently held (0..Cap()).
func (r *Ring[T]) Len() int { return r.size }

// Append stores `item` tagged with `seq`. If the ring is full the oldest
// entry is overwritten. seq is expected to be monotonically increasing
// across calls; that invariant is the responsibility of the room.
func (r *Ring[T]) Append(seq uint64, item T) {
	r.items[r.head] = item
	r.seqs[r.head] = seq
	r.head = (r.head + 1) % cap(r.items)
	if r.size < cap(r.items) {
		r.size++
	}
}

// AfterSeq returns every stored item whose tag-seq is strictly greater than
// `lastSeq`, in ascending seq order. The bool result is true when the caller
// can rely on the slice being a complete replay; it is false when `lastSeq`
// is older than the oldest entry the ring still holds (i.e., the caller MUST
// fall back to a fresh hydration because some events are gone).
//
// Special cases:
//   - If the ring is empty, returns (nil, true): there's nothing to replay,
//     but no gap either; the caller stays subscribed and waits for live events.
//   - If lastSeq >= the most recent seq, returns (nil, true).
//   - If lastSeq < oldest stored seq, returns (nil, false): gap too large.
func (r *Ring[T]) AfterSeq(lastSeq uint64) ([]T, bool) {
	if r.size == 0 {
		return nil, true
	}

	// Walk the ring from oldest to newest. Oldest index is head-size (mod cap).
	c := cap(r.items)
	oldestIdx := (r.head - r.size + c) % c
	oldestSeq := r.seqs[oldestIdx]

	// Gap: client wants events older than the oldest we still hold.
	// The "lastSeq < oldestSeq - 1" check would allow exact-boundary replay;
	// since we want strictly-greater-than, lastSeq+1 must be >= oldestSeq.
	if lastSeq+1 < oldestSeq {
		return nil, false
	}

	var out []T
	for i := 0; i < r.size; i++ {
		idx := (oldestIdx + i) % c
		if r.seqs[idx] > lastSeq {
			if out == nil {
				out = make([]T, 0, r.size-i)
			}
			out = append(out, r.items[idx])
		}
	}
	return out, true
}
