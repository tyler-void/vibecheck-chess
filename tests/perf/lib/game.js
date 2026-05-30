// HTTP helpers for the bare-chess k6 perf harness. Vanilla k6 ES modules.
//
// Contract reference: specs/001-kiss-chess-engine/contracts/http-api.md
//
//   POST /games
//     body: {
//       "white":  PlayerID,
//       "black":  PlayerID,
//       "time_control": { "initial_ms": int, "increment_ms": int, "delay_ms": int }
//     }
//     resp: { "game_id": ..., "snapshot": BoardSnapshot }
//
//   POST /games/{id}/move
//     header X-Player-ID: <player>
//     body:   { "seq": uint64, "move": "e2e4", "client_action_id": "<uuid>", "claim_draw": bool }
//     resp:   204 fresh accept | 200 idempotent retry | 409 conflict | 400/422 bad
//
//   GET /games/{id}/snapshot
//     resp: { "game_id": ..., "snapshot": BoardSnapshot }
//
//   GET /games/{id}/sse  — see lib/sse.js
//
// PlayerID is sent via X-Player-ID header on per-move requests. There is
// NO authentication in the engine itself — the upstream gateway is
// expected to validate identity and forward it via this header.

import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

export const moveAccepted = new Counter('move_accepted');
export const moveStaleSeq = new Counter('move_stale_seq');
export const moveRateLimited = new Counter('move_rate_limited');
export const moveErrored = new Counter('move_errored');
export const idempotencyDoubleApply = new Rate('idempotency_double_apply');
export const staleSeqDoubleCommit = new Rate('stale_seq_double_commit');
export const illegalMoveAccepted = new Rate('illegal_move_accepted');
export const silentMoveLoss = new Rate('silent_move_loss');
export const ambiguousMoveOutcome = new Rate('ambiguous_move_outcome');
export const flagAdjudicationLagMs = new Trend('flag_adjudication_lag_ms', true);

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// playerHeaders returns the headers used for player-attributed requests
// (move / resign / draw-offer). The X-Player-ID header is the spec's
// placeholder for "the gateway has authenticated this caller".
export function playerHeaders(playerID) {
  return {
    'X-Player-ID': playerID,
    'Accept': 'application/json',
    'Content-Type': 'application/json',
  };
}

// publicHeaders for endpoints that don't need a player identity
// (create-game, snapshot, sse).
export function publicHeaders() {
  return {
    'Accept': 'application/json',
    'Content-Type': 'application/json',
  };
}

// Translate a friendly time-control name into the wire shape per the API
// contract.
const PRESETS = {
  bullet: { initial_ms: 60 * 1000, increment_ms: 0, delay_ms: 0 },
  blitz:  { initial_ms: 3 * 60 * 1000, increment_ms: 2000, delay_ms: 0 },
  rapid:  { initial_ms: 10 * 60 * 1000, increment_ms: 5000, delay_ms: 0 },
  long:   { initial_ms: 30 * 60 * 1000, increment_ms: 0, delay_ms: 0 },
};

// createGame POSTs to /games and returns { gameID, status, snapshot }.
//
// opts:
//   timeControl:   "bullet" | "blitz" | "rapid" | "long" | { initial_ms, increment_ms, delay_ms }
//   white, black:  player IDs (default: "perf-white", "perf-black")
//   vuTag:         appended to player IDs for cardinality in metrics
export function createGame(opts) {
  opts = opts || {};
  let tc = opts.timeControl || 'blitz';
  if (typeof tc === 'string') tc = PRESETS[tc] || PRESETS.blitz;
  const white = (opts.white || 'perf-white') + (opts.vuTag ? '-' + opts.vuTag : '');
  const black = (opts.black || 'perf-black') + (opts.vuTag ? '-' + opts.vuTag : '');

  const body = JSON.stringify({
    white: white,
    black: black,
    time_control: tc,
  });
  const res = http.post(BASE_URL + '/games', body, {
    headers: publicHeaders(),
    tags: { op: 'create_game' },
  });
  if (res.status !== 201) {
    return { gameID: null, status: res.status, snapshot: null, white: white, black: black };
  }
  let parsed = null;
  try {
    parsed = res.json();
  } catch (e) {
    return { gameID: null, status: res.status, snapshot: null, white: white, black: black };
  }
  return {
    gameID: parsed && parsed.game_id,
    status: res.status,
    snapshot: parsed && parseSnapshot(parsed.snapshot),
    white: white,
    black: black,
  };
}

// submitMove POSTs to /games/{id}/move and classifies the result.
//
// Returns { status, accepted, stale, rateLimited, errored, idempotentRetry,
//           snapshot, durationMs, body }.
//
// Acceptance status mapping per contracts/http-api.md:
//   204 → fresh accept (state lands on the SSE stream)
//   200 → idempotent retry (same client_action_id as a prior accepted move)
//   409 → conflict (stale_sequence | not_your_turn | game_over | flagged)
//   400 → bad_move_syntax | invalid_request
//   422 → illegal_move
//   429 → rate-limited (not currently emitted by the server; reserved)
export function submitMove(gameID, seq, moveUCI, clientActionID, opts) {
  opts = opts || {};
  const playerID = opts.playerID || ((opts.vuTag ? 'perf-white-' + opts.vuTag : 'perf-white'));

  const body = JSON.stringify({
    seq: seq,
    move: moveUCI,
    client_action_id: clientActionID,
    claim_draw: !!opts.claimDraw,
  });

  const tags = Object.assign({ op: 'move' }, opts.tags || {});
  const params = { headers: playerHeaders(playerID), tags: tags };

  const t0 = Date.now();
  const res = http.post(BASE_URL + '/games/' + gameID + '/move', body, params);
  const durationMs = Date.now() - t0;

  const accepted = res.status === 204;
  const idempotentRetry = res.status === 200;
  const stale = res.status === 409;
  const rateLimited = res.status === 429;
  const errored = !accepted && !idempotentRetry && !stale && !rateLimited;

  if (accepted) moveAccepted.add(1, tags);
  if (stale) moveStaleSeq.add(1, tags);
  if (rateLimited) moveRateLimited.add(1, tags);
  if (errored) moveErrored.add(1, tags);

  check(res, {
    'move status acceptable': function (r) {
      return r.status === 204 || r.status === 200 || r.status === 409 ||
             r.status === 422 || r.status === 400 || r.status === 429;
    },
  }, tags);

  // Snapshots are available on stale (envelope.error.current_snapshot)
  // and on idempotent retry (response body). Fresh accept is 204 with
  // no body — the SSE stream carries the new state.
  let snapshot = null;
  if (stale) {
    try {
      const env = res.json();
      if (env && env.error && env.error.current_snapshot) {
        snapshot = parseSnapshot(env.error.current_snapshot);
      }
    } catch (e) { /* ignore */ }
  } else if (idempotentRetry && opts.parseRetryBody !== false) {
    try {
      // JSON-fallback retry body shape: { "result": "ok", "seq": <post-action-seq> }
      // No snapshot here; the seq is enough for the caller to advance.
      const j = res.json();
      if (j && typeof j.seq === 'number') {
        snapshot = { seq: j.seq };
      }
    } catch (e) { /* ignore */ }
  }

  return {
    status: res.status,
    accepted: accepted,
    idempotentRetry: idempotentRetry,
    stale: stale,
    rateLimited: rateLimited,
    errored: errored,
    snapshot: snapshot,
    durationMs: durationMs,
    body: res.body,
  };
}

// getSnapshot fetches /games/{id}/snapshot and returns { status, snapshot }.
export function getSnapshot(gameID, opts) {
  opts = opts || {};
  const res = http.get(BASE_URL + '/games/' + gameID + '/snapshot', {
    headers: publicHeaders(),
    tags: Object.assign({ op: 'snapshot' }, opts.tags || {}),
  });
  if (res.status !== 200) {
    return { status: res.status, snapshot: null };
  }
  let snap = null;
  try {
    const j = res.json();
    snap = parseSnapshot(j && j.snapshot);
  } catch (e) {
    snap = null;
  }
  return { status: res.status, snapshot: snap };
}

// submitResign / submitDrawOffer / submitDrawAccept — the three non-move
// state-changing actions per FR-036 / FR-037. All share the action body
// shape { seq, client_action_id } and the same response status grammar
// as submitMove.
function postAction(gameID, endpoint, playerID, seq, clientActionID, opts) {
  opts = opts || {};
  const body = JSON.stringify({ seq: seq, client_action_id: clientActionID });
  const tags = Object.assign({ op: endpoint }, opts.tags || {});
  const params = { headers: playerHeaders(playerID), tags: tags };
  const res = http.post(BASE_URL + '/games/' + gameID + '/' + endpoint, body, params);
  return {
    status: res.status,
    accepted: res.status === 204,
    idempotentRetry: res.status === 200,
    conflict: res.status === 409,
    body: res.body,
  };
}
export function submitResign(gameID, playerID, seq, clientActionID, opts) {
  return postAction(gameID, 'resign', playerID, seq, clientActionID, opts);
}
export function submitDrawOffer(gameID, playerID, seq, clientActionID, opts) {
  return postAction(gameID, 'draw-offer', playerID, seq, clientActionID, opts);
}
export function submitDrawAccept(gameID, playerID, seq, clientActionID, opts) {
  return postAction(gameID, 'draw-offer/accept', playerID, seq, clientActionID, opts);
}

// parseSnapshot normalises a BoardSnapshot from the JSON wire shape per
// data-model.md rev 2 (no `Changed []SquareDelta`; `draw_offer_from`
// string instead). PascalCase keys are tolerated for ad-hoc seeding.
export function parseSnapshot(obj) {
  if (!obj) return null;
  return {
    seq: pick(obj, ['seq', 'Seq']),
    turn: pick(obj, ['turn', 'Turn']),
    outcome: pick(obj, ['outcome', 'Outcome']),
    termination: pick(obj, ['termination', 'Termination']),
    whiteMS: pick(obj, ['white_ms', 'WhiteMS']),
    blackMS: pick(obj, ['black_ms', 'BlackMS']),
    lastMove: pick(obj, ['last_move', 'LastMove']),
    fen: pick(obj, ['fen', 'FEN']),
    drawOfferFrom: pick(obj, ['draw_offer_from', 'DrawOfferFrom']) || '',
    serverUnixMs: pick(obj, ['server_unix_ms', 'ServerUnixMS']),
    activeExpiresAtUnixMs: pick(obj, ['active_expires_at_unix_ms', 'ActiveExpiresAtUnixMS']),
  };
}

function pick(obj, keys) {
  for (let i = 0; i < keys.length; i++) {
    if (obj[keys[i]] !== undefined) return obj[keys[i]];
  }
  return undefined;
}

// A tiny pool of UCI moves we can cycle through without engine state.
// Scenarios that need real legal play across many plies should drive from
// snapshot.fen + a real generator; this set is intentionally trivial.
const SAMPLE_MOVES = [
  'e2e4', 'e7e5', 'g1f3', 'b8c6', 'f1c4', 'f8c5',
  'd2d3', 'd7d6', 'c2c3', 'g8f6', 'b1d2', 'e8g8',
];
export function sampleMove(i) {
  return SAMPLE_MOVES[i % SAMPLE_MOVES.length];
}

// Intentionally-malformed / illegal payloads for the concurrency-correctness
// scenario. Server MUST reject every one of these.
export const ILLEGAL_MOVES = [
  'e2e5',  // illegal pawn move (jumps too far)
  'a1a8',  // jumping rook through pieces
  'e1e3',  // king teleport
  'e2e4q', // promotion suffix on non-promotion move
  'e7e8k', // king promotion (regex catches this)
  'e2-e4', // wrong notation
  'Nf3',   // SAN
  'e2e9',  // off-board rank
  'z2z4',  // off-board file
  '',      // empty
];

// genClientActionID returns a per-attempt idempotency key. Reuse the same
// key when intentionally testing idempotent retries.
export function genClientActionID(vu, iter, attempt) {
  return 'cmid-' + vu + '-' + iter + '-' + (attempt || 0);
}
