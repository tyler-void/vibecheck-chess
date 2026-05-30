// HTTP helpers for the bare-chess perf harness. Vanilla k6 ES modules.
//
// Contract reference (from the feature spec):
//
//   POST /games/{id}/move
//     body (<=512 bytes, DisallowUnknownFields):
//       {
//         "game_id":        string,
//         "seq":            uint64,
//         "move":           string (UCI long algebraic),
//         "client_action_id": string (idempotency key),
//         "claim_draw":     bool
//       }
//     PlayerID is NOT in the body; the server resolves it from auth.
//
//     Acceptable response statuses for perf purposes:
//       200, 202 -> committed
//       409      -> stale seq (server returns latest snapshot)
//       429      -> rate-limited
//     Anything else is a real failure and is recorded.
//
//   GET /games/{id}/snapshot
//     -> BoardSnapshot JSON: { Seq, Turn, Outcome, WhiteMS, BlackMS,
//                              LastMove, FEN, Changed, ServerUnix }
//
//   GET /games/{id}/sse  -> see lib/sse.js
//
// The reference design does NOT expose a public game-creation HTTP
// endpoint (rooms are created in-process via GameManager.Create). For
// perf testing we assume a test-only seeding route:
//
//   POST /test/games
//     body: { "time_control": "blitz" | "bullet" | "rapid" | "30+0" | ... }
//     resp: { "game_id": "<id>" }
//
// If your server uses a different path, change BASE paths below.

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

// Auth header for the synthetic VU. The server is expected to map this
// to a stable PlayerID in test mode. See README "Caveats".
export function authHeaders(vuTag) {
  return {
    Authorization: 'Bearer perf-vu-' + (vuTag || 'anon'),
    'Content-Type': 'application/json',
  };
}

// createGame asks the test-only seeding endpoint for a new game id.
// Returns { gameID, status } — gameID is null on failure.
export function createGame(opts) {
  opts = opts || {};
  const body = JSON.stringify({
    time_control: opts.timeControl || 'blitz',
    white: opts.white || 'perf-white',
    black: opts.black || 'perf-black',
  });
  const res = http.post(BASE_URL + '/test/games', body, {
    headers: authHeaders(opts.vuTag),
    tags: { op: 'create_game' },
  });
  if (res.status !== 200 && res.status !== 201) {
    return { gameID: null, status: res.status };
  }
  let parsed = null;
  try {
    parsed = res.json();
  } catch (e) {
    return { gameID: null, status: res.status };
  }
  return {
    gameID: parsed && (parsed.game_id || parsed.GameID || parsed.id),
    status: res.status,
  };
}

// submitMove POSTs a move to /games/{id}/move and classifies the result.
// Returns { status, body, accepted, stale, rateLimited, errored, snapshot, durationMs }.
export function submitMove(gameID, seq, moveUCI, clientActionID, opts) {
  opts = opts || {};
  const payload = {
    game_id: gameID,
    seq: seq,
    move: moveUCI,
    client_action_id: clientActionID,
    claim_draw: !!opts.claimDraw,
  };
  const body = JSON.stringify(payload);
  // 512-byte body limit on the server — sanity check on the client too.
  if (body.length > 512) {
    moveErrored.add(1, opts.tags || {});
    return {
      status: 0,
      accepted: false,
      stale: false,
      rateLimited: false,
      errored: true,
      snapshot: null,
      durationMs: 0,
    };
  }

  const tags = Object.assign({ op: 'move' }, opts.tags || {});
  const params = { headers: authHeaders(opts.vuTag), tags: tags };
  const t0 = Date.now();
  const res = http.post(BASE_URL + '/games/' + gameID + '/move', body, params);
  const durationMs = Date.now() - t0;

  const accepted = res.status === 200 || res.status === 202;
  const stale = res.status === 409;
  const rateLimited = res.status === 429;
  const errored = !accepted && !stale && !rateLimited;

  if (accepted) moveAccepted.add(1, tags);
  if (stale) moveStaleSeq.add(1, tags);
  if (rateLimited) moveRateLimited.add(1, tags);
  if (errored) moveErrored.add(1, tags);

  check(res, {
    'move status acceptable': function (r) {
      return r.status === 200 || r.status === 202 || r.status === 409 || r.status === 429;
    },
  }, tags);

  let snapshot = null;
  if (accepted || stale) {
    try {
      const j = res.json();
      snapshot = parseSnapshot(j);
    } catch (e) {
      snapshot = null;
    }
  }

  return {
    status: res.status,
    accepted: accepted,
    stale: stale,
    rateLimited: rateLimited,
    errored: errored,
    snapshot: snapshot,
    durationMs: durationMs,
    body: res.body,
  };
}

// getSnapshot fetches /games/{id}/snapshot and returns a parsed BoardSnapshot.
export function getSnapshot(gameID, opts) {
  opts = opts || {};
  const res = http.get(BASE_URL + '/games/' + gameID + '/snapshot', {
    headers: authHeaders(opts.vuTag),
    tags: Object.assign({ op: 'snapshot' }, opts.tags || {}),
  });
  if (res.status !== 200) {
    return { status: res.status, snapshot: null };
  }
  let snap = null;
  try {
    snap = parseSnapshot(res.json());
  } catch (e) {
    snap = null;
  }
  return { status: res.status, snapshot: snap };
}

// parseSnapshot normalises a BoardSnapshot from either Go-style PascalCase
// or JSON-style snake_case so scenarios don't need to care.
export function parseSnapshot(obj) {
  if (!obj) return null;
  return {
    seq: pick(obj, ['Seq', 'seq']),
    turn: pick(obj, ['Turn', 'turn']),
    outcome: pick(obj, ['Outcome', 'outcome']),
    whiteMS: pick(obj, ['WhiteMS', 'white_ms']),
    blackMS: pick(obj, ['BlackMS', 'black_ms']),
    lastMove: pick(obj, ['LastMove', 'last_move']),
    fen: pick(obj, ['FEN', 'fen']),
    changed: pick(obj, ['Changed', 'changed']) || [],
    serverUnix: pick(obj, ['ServerUnix', 'server_unix', 'server_unix_ms']),
  };
}

function pick(obj, keys) {
  for (let i = 0; i < keys.length; i++) {
    if (obj[keys[i]] !== undefined) return obj[keys[i]];
  }
  return undefined;
}

// A tiny pool of UCI moves we can cycle through without engine state.
// Scenarios that need real legal moves should drive from snapshot.fen
// + a real generator; this set is intentionally trivial and is used
// for load shape only.
const SAMPLE_MOVES = [
  'e2e4', 'e7e5', 'g1f3', 'b8c6', 'f1c4', 'f8c5',
  'd2d3', 'd7d6', 'c2c3', 'g8f6', 'b1d2', 'e8g8',
];

export function sampleMove(i) {
  return SAMPLE_MOVES[i % SAMPLE_MOVES.length];
}

// A handful of intentionally-malformed / illegal payloads for the
// concurrency-correctness scenario. Server MUST reject all of these.
export const ILLEGAL_MOVES = [
  'e2e5',        // not a legal pawn move
  'a1a8',        // jumping rook through pieces from start position
  'e1e3',        // king teleport
  'e2e4q',       // promotion suffix on a non-promotion move
  'e7e8k',       // illegal promotion piece
  'e2-e4',       // not UCI
  'Nf3',         // SAN, not UCI
  'e2e9',        // off-board
  'z2z4',        // off-board file
  '',            // empty
];

// genClientActionID returns a per-attempt idempotency key. Reuse the same
// key when intentionally testing idempotent retries.
export function genClientActionID(vu, iter, attempt) {
  return 'cmid-' + vu + '-' + iter + '-' + (attempt || 0);
}
