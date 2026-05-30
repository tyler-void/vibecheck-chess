// Package sse owns the SSE fan-out plumbing: one FanoutHub per game, plus
// per-connection SSEClient wrappers with bounded send queues and a
// drop-the-slow-reader policy (Constitution IV).
//
// Lock-ordering invariant (Constitution III): FanoutHub.mu protects the
// subscriber map ONLY. It MUST NOT be held while writing to a network
// socket. The mutating goroutine in internal/chess never waits on network
// I/O.
package sse

import (
	"sync"
	"sync/atomic"

	"kisschess/internal/chess"
)

// sendBufferSize is the per-subscriber outbound queue capacity. Sized to
// cover roughly 16 moves of headroom at typical play cadence; one missed
// publish attempt and the client is dropped (Constitution IV).
const sendBufferSize = 16

// SSEClient is one subscriber attached to one game's FanoutHub. Created by
// Hub.Register and owned by that hub until Unregister.
//
// Send is the buffered outbound channel. The HTTP handler ranges over Send
// and writes to the response. When the hub drops a slow reader it closes
// Send; the handler then exits its range loop and tears down the connection.
type SSEClient struct {
	ID      chess.ClientID
	Send    chan []byte
	LastSeq atomic.Uint64 // best-effort, for /metrics and diagnostics
	once    sync.Once
}

// close is idempotent and safe to call from the hub or the handler. The
// channel close MUST happen exactly once.
func (c *SSEClient) close() {
	c.once.Do(func() {
		close(c.Send)
	})
}
