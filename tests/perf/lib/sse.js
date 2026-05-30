// Minimal SSE consumer for k6 perf tests against the kisschess server.
//
// The server emits Datastar v1.0+ wire frames (per
// specs/001-kiss-chess-engine/contracts/sse-events.md):
//
//   event: datastar-patch-signals
//   id: <seq>
//   data: signals {"seq":42,"server_unix_ms":...,"white_ms":...,...}
//
// We parse the `data:` line by stripping the leading "signals " (8 chars)
// to recover the JSON snapshot projection. `datastar-patch-elements`
// frames carry HTML rather than JSON and are ignored for metrics —
// they're used for hydration / move-list rendering, not for per-event
// state we want to measure.
//
// Requires a k6 binary built with the phymbert/xk6-sse extension. The
// Makefile target `make $(PERF_BIN)` runs `xk6 build --with
// github.com/phymbert/xk6-sse@latest`.

import sse from 'k6/x/sse';
import { Trend, Rate, Counter } from 'k6/metrics';

// Custom metrics referenced by scenario thresholds.
export const sseEventLagMs = new Trend('sse_event_lag_ms', true);
export const sseConnectionDroppedIdle = new Rate('sse_connection_dropped_idle');
export const sseReconnectSuccessZeroGap = new Rate('sse_reconnect_success_zero_gap');
export const sseDuplicateEvent = new Rate('sse_duplicate_event');
export const sseSequenceGap = new Rate('sse_sequence_gap');
export const sseEventsReceived = new Counter('sse_events_received');

// Datastar event names per the v1.0+ wire spec.
const EVT_SIGNALS = 'datastar-patch-signals';
const EVT_ELEMENTS = 'datastar-patch-elements';

// subscribe(url, opts) -> opens one SSE stream and invokes opts.onEvent
// for each parsed signal frame. Returns when the stream closes.
//
// opts:
//   headers:        extra HTTP headers (e.g. Last-Event-ID)
//   tags:           k6 tag map applied to emitted metrics
//   onEvent(evt):   called for each parsed snapshot signal patch
//   onOpen():       called once when the connection is established
//   onError(err):   called on transport / parse errors
//   slowReader:     if true, busy-wait ~1s between events (for hot-match
//                   fanout scenarios that need slow VUs)
//   maxEvents:      stop after N signal events
//
// The event object passed to onEvent is:
//   { id, lastEventID, seq, raw, recvUnixMs }
// where `raw` is the parsed JSON object from the `data: signals ...` line.
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

      // Only interested in signal patches — elements patches carry HTML
      // hydration that doesn't move our SLO metrics.
      if (event.name !== EVT_SIGNALS) {
        // Track received-events count for visibility but do nothing else.
        if (event.name === EVT_ELEMENTS) {
          sseEventsReceived.add(1, Object.assign({ kind: 'elements' }, opts.tags || {}));
        }
        return;
      }

      // `event.data` for signal patches is exactly: "signals " + <single-line JSON>.
      // Strip the leading "signals " (8 chars) before parsing.
      let payload = event.data || '';
      if (payload.startsWith('signals ')) {
        payload = payload.slice('signals '.length);
      }

      let parsed = null;
      try {
        parsed = JSON.parse(payload);
      } catch (e) {
        return;
      }

      sseEventsReceived.add(1, Object.assign({ kind: 'signals' }, opts.tags || {}));

      // Lag from server-stamped time → client receive.
      if (parsed && typeof parsed.server_unix_ms === 'number') {
        sseEventLagMs.add(recvUnixMs - parsed.server_unix_ms, opts.tags || {});
      }

      // Sequence-gap / duplicate detection.
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

      // Slow-reader simulation for hot-match-fanout scenarios. Busy-wait
      // inside the event handler (k6's sleep() blocks the iteration
      // scheduler too aggressively).
      if (opts.slowReader) {
        const target = recvUnixMs + 1000;
        while (Date.now() < target) {
          /* spin */
        }
      }
    });

    client.on('error', function (e) {
      if (opts.onError) opts.onError(e);
    });
  });

  return res;
}

// lastEventIDHeader returns a header object suitable for resuming an SSE
// stream from a known seq. Pass null/undefined to omit.
export function lastEventIDHeader(seq) {
  if (seq === null || seq === undefined) return {};
  return { 'Last-Event-ID': String(seq) };
}
