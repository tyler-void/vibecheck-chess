// Minimal SSE consumer for k6 perf tests.
//
// Uses k6's experimental SSE module:
//   https://grafana.com/docs/k6/latest/javascript-api/k6-experimental/sse/
//
// If your k6 build was compiled without the experimental SSE module
// (rare on recent versions >= v0.47), fall back to the http-stream
// approach sketched at the bottom of this file. Vanilla JS only.
//
// Each k6 VU calling subscribe() opens one SSE connection. Per the
// system constraints (1-3 SSE connections per user per game), do NOT
// multiplex many SSE streams inside a single VU iteration.

import sse from 'k6/experimental/sse';
import { Trend, Rate, Counter } from 'k6/metrics';

// Custom metrics referenced by scenario thresholds. Keeping names
// stable here so scenarios can tag and filter consistently.
export const sseEventLagMs = new Trend('sse_event_lag_ms', true);
export const sseConnectionDroppedIdle = new Rate('sse_connection_dropped_idle');
export const sseReconnectSuccessZeroGap = new Rate('sse_reconnect_success_zero_gap');
export const sseDuplicateEvent = new Rate('sse_duplicate_event');
export const sseSequenceGap = new Rate('sse_sequence_gap');
export const sseEventsReceived = new Counter('sse_events_received');

// Goroutine/heap bounded checks are emitted as k6 checks in scenarios
// (so they show up under `checks{check:goroutines_bounded}` etc.). We
// expose helpers here for the metric names only.

// subscribe(url, opts) -> opens an SSE stream and invokes opts.onEvent
// for each parsed board event. Returns when the stream closes.
//
// opts:
//   headers:        extra HTTP headers (e.g. Last-Event-ID, Authorization)
//   tags:           k6 tag map applied to emitted metrics
//   onEvent(evt):   called for each parsed JSON SSE data frame
//   onOpen():       called once when the connection is established
//   onError(err):   called on transport / parse errors
//   slowReader:     if true, sleep ~1s between events (for hot-match)
//   maxEvents:      stop after N events (default: unbounded; rely on test duration)
//
// The event object passed to onEvent is:
//   { id: string, lastEventID: string, seq: number, raw: object, recvUnixMs: number }
export function subscribe(url, opts) {
  opts = opts || {};
  const headers = Object.assign(
    { Accept: 'text/event-stream' },
    opts.headers || {},
  );
  const params = { headers: headers, tags: opts.tags || {} };

  let count = 0;
  let lastSeq = -1;
  const seen = {};

  const res = sse.open(url, params, function (client) {
    client.on('open', function () {
      if (opts.onOpen) opts.onOpen();
    });

    client.on('event', function (event) {
      const recvUnixMs = Date.now();
      let parsed = null;
      try {
        parsed = JSON.parse(event.data);
      } catch (e) {
        // Non-JSON frames (e.g. keepalive comments) are silently ignored.
        return;
      }

      sseEventsReceived.add(1, opts.tags || {});

      // Track lag from server_unix_ms -> client receive
      if (parsed && typeof parsed.server_unix_ms === 'number') {
        sseEventLagMs.add(recvUnixMs - parsed.server_unix_ms, opts.tags || {});
      }

      // Sequence gap / duplicate detection.
      if (parsed && typeof parsed.seq === 'number') {
        if (seen[parsed.seq]) {
          sseDuplicateEvent.add(1, opts.tags || {});
        } else {
          seen[parsed.seq] = true;
          if (lastSeq >= 0 && parsed.seq !== lastSeq + 1) {
            sseSequenceGap.add(1, opts.tags || {});
          } else {
            sseSequenceGap.add(0, opts.tags || {});
          }
          lastSeq = parsed.seq;
        }
      }

      if (opts.onEvent) {
        opts.onEvent({
          id: event.id,
          lastEventID: event.id,
          seq: parsed && parsed.seq,
          raw: parsed,
          recvUnixMs: recvUnixMs,
        });
      }

      count++;
      if (opts.maxEvents && count >= opts.maxEvents) {
        client.close();
      }

      // Slow-reader simulation: throttle to ~1 event/sec by busy-waiting
      // a small amount inside the event handler. We avoid k6's `sleep`
      // here because it would block the iteration scheduler too.
      if (opts.slowReader) {
        const target = recvUnixMs + 1000;
        while (Date.now() < target) {
          /* spin briefly */
        }
      }
    });

    client.on('error', function (e) {
      if (opts.onError) opts.onError(e);
    });
  });

  return res;
}

// lastEventIDHeader returns a header object suitable for resuming an
// SSE stream from a known seq. Pass null/undefined to omit.
export function lastEventIDHeader(seq) {
  if (seq === null || seq === undefined) return {};
  return { 'Last-Event-ID': String(seq) };
}

// -----------------------------------------------------------------------
// Fallback (informational only — not wired): if k6/experimental/sse is
// unavailable, you can implement SSE consumption with k6/http by issuing
// a normal GET against /games/{id}/sse and reading the response body in
// streaming mode. k6's standard http module does not expose chunked
// streaming, so the practical fallback is to:
//
//   1. Build a custom k6 binary (xk6) including the `xk6-sse` extension.
//   2. Or, use a WebSocket bridge: `k6/ws` against a `/games/{id}/ws`
//      endpoint that mirrors SSE events.
//   3. Or, poll `/games/{id}/snapshot` at a fixed cadence (loses lag SLO
//      meaning — only useful as a smoke test).
//
// We deliberately do NOT implement option 3 here because it would
// silently mask SSE-specific bugs (sequence gaps, slow-reader fanout,
// reconnect-resume). Prefer keeping the experimental module.
