package sse

import (
	"sync"
	"sync/atomic"
)

// FanoutHub is the per-game broadcaster. Owns the set of currently connected
// subscribers and exposes non-blocking Publish (Constitution IV).
//
// Concurrency: mu protects ONLY the clients map and nextID counter. Publish
// holds an RLock while iterating, but the channel send is non-blocking
// (select+default), so the lock-hold window is independent of subscriber
// I/O speed. mu is never held while writing to a socket — that's the
// handler's job, downstream of the channel receive.
type FanoutHub struct {
	mu       sync.RWMutex
	clients  map[ClientID]*SSEClient
	nextID   atomic.Uint64
	dropped  atomic.Uint64 // total slow-reader drops, exported via Hub.DroppedCount
}

// NewFanoutHub constructs an empty hub.
func NewFanoutHub() *FanoutHub {
	return &FanoutHub{
		clients: make(map[ClientID]*SSEClient),
	}
}

// Register adds a new subscriber and returns its handle. The caller MUST
// eventually call Unregister(client.ID) — typically via a `defer` on the
// HTTP handler — to release the slot.
func (h *FanoutHub) Register() *SSEClient {
	id := ClientID(h.nextID.Add(1))
	c := &SSEClient{
		ID:   id,
		Send: make(chan []byte, sendBufferSize),
	}
	h.mu.Lock()
	h.clients[id] = c
	h.mu.Unlock()
	return c
}

// Unregister removes a subscriber and closes its Send channel. Safe to call
// even if the subscriber is not in the map (idempotent).
func (h *FanoutHub) Unregister(id ClientID) {
	h.mu.Lock()
	c, ok := h.clients[id]
	if ok {
		delete(h.clients, id)
	}
	h.mu.Unlock()
	if ok {
		c.close()
	}
}

// Publish sends `payload` to every current subscriber. Non-blocking per
// subscriber: if the subscriber's Send queue is full, that subscriber is
// dropped immediately (slow-reader policy, Constitution IV / FR-028).
//
// The same []byte is sent to every subscriber — callers MUST NOT mutate the
// slice after calling Publish ("render once, broadcast same bytes",
// Constitution V).
func (h *FanoutHub) Publish(payload []byte) {
	// Collect victims under the read lock; close them outside the lock to
	// keep the lock-hold window short and predictable.
	var victims []*SSEClient
	h.mu.RLock()
	for _, c := range h.clients {
		select {
		case c.Send <- payload:
			// delivered
		default:
			victims = append(victims, c)
		}
	}
	h.mu.RUnlock()

	if len(victims) == 0 {
		return
	}

	// Promote to write lock to delete; channel-close is idempotent.
	h.mu.Lock()
	for _, c := range victims {
		if _, ok := h.clients[c.ID]; ok {
			delete(h.clients, c.ID)
			h.dropped.Add(1)
		}
	}
	h.mu.Unlock()
	for _, c := range victims {
		c.close()
	}
}

// Count returns the current number of subscribers. Cheap, lock-bounded.
func (h *FanoutHub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// DroppedCount returns the cumulative number of subscribers dropped due to
// slow-reader backpressure. Exported for kiss_sse_dropped_total{reason="slow"}.
func (h *FanoutHub) DroppedCount() uint64 {
	return h.dropped.Load()
}
