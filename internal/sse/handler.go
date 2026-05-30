package sse

import (
	stdhttp "net/http"
	"strconv"
	"time"
)

// KeepAliveInterval is how often we emit a `: ping\n\n` comment line on
// every open SSE stream. Constitution IV / FR-026 / research R-7. 15s is
// well inside common proxy idle-timeout floors (nginx default 60s,
// Cloudflare ~100s, AWS ALB default 60s).
const KeepAliveInterval = 15 * time.Second

// WriteDeadline is the per-write deadline used via ResponseController. If a
// write to a subscriber blocks for longer than this — typical of a TCP
// receive-window collapse — we treat the subscriber as gone and exit the
// handler. The hub will drop them on the next publish anyway, but this
// keeps the handler goroutine from sitting on a wedged socket.
const WriteDeadline = 3 * time.Second

// Room is the minimal contract the SSE handler needs from a chess.GameRoom.
// Defined here as an interface so internal/sse stays free of an import
// cycle to internal/chess.
type Room interface {
	Hub() *FanoutHub
	ReplayOrSnapshot(lastSeq uint64, now time.Time) (replay [][]byte, hydration []byte)
}

// SubscribeHandler serves GET /games/{id}/sse. Lifecycle:
//
//  1. Set SSE headers (Content-Type, Cache-Control, Connection,
//     X-Accel-Buffering); gzip stays disabled because the Go HTTP server
//     doesn't compress responses by default.
//  2. Parse Last-Event-ID (FR-024 / FR-025). On malformed input, treat it
//     as missing (fresh hydration) per the U1 remediation — never 500.
//  3. Call room.ReplayOrSnapshot — either replay missed events from the
//     ring or write a fresh hydration patch.
//  4. Register with the hub.
//  5. Loop: select { Send | ticker | request context }. Write each event,
//     flush, repeat. Emit `: ping\n\n` every KeepAliveInterval.
//  6. On any exit path: defer Unregister so the hub releases the slot.
//
// Lock-order discipline (Constitution III): we hold NO state locks during
// any write. The hub publishes to a buffered channel; the handler reads
// from the channel and writes to the socket — these are orthogonal.
func SubscribeHandler(room Room, now func() time.Time) stdhttp.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		rc := stdhttp.NewResponseController(w)

		// SSE headers per research R-7.
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(stdhttp.StatusOK)
		// Flush headers so the client sees a "live" response immediately,
		// not waiting on the first event.
		if err := rc.Flush(); err != nil {
			return
		}

		// 2. Last-Event-ID parsing. Malformed → fresh hydration.
		lastSeq, _ := parseLastEventID(r.Header.Get("Last-Event-ID"))

		// 3. Replay or hydrate.
		replay, hydration := room.ReplayOrSnapshot(lastSeq, now())
		writeAndFlush := func(b []byte) error {
			_ = rc.SetWriteDeadline(now().Add(WriteDeadline))
			if _, err := w.Write(b); err != nil {
				return err
			}
			return rc.Flush()
		}

		if hydration != nil {
			if err := writeAndFlush(hydration); err != nil {
				return
			}
		}
		for _, evt := range replay {
			if err := writeAndFlush(evt); err != nil {
				return
			}
		}

		// 4. Register with hub. defer Unregister covers all exit paths
		// including panics — Constitution: every per-subscriber resource
		// MUST be released on disconnect (FR-029).
		client := room.Hub().Register()
		defer room.Hub().Unregister(client.ID)

		// 5. Main loop.
		ticker := time.NewTicker(KeepAliveInterval)
		defer ticker.Stop()

		ctx := r.Context()
		ping := []byte(": ping\n\n")

		for {
			select {
			case msg, ok := <-client.Send:
				if !ok {
					// Hub dropped us as a slow reader; close out.
					return
				}
				if err := writeAndFlush(msg); err != nil {
					return
				}
			case <-ticker.C:
				if err := writeAndFlush(ping); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

// parseLastEventID parses the Last-Event-ID header. Empty or non-numeric
// input returns (0, false) so the caller can treat it as "send fresh
// hydration" (U1 remediation — must NEVER return 500 for malformed input).
func parseLastEventID(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
