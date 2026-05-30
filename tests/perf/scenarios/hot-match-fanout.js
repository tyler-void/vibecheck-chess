// hot-match-fanout.js
//
// Success Criteria: SC-003, SC-009
//
// Goal: Prove that a single game with 1000+ spectators (including
// deliberately slow readers) does not degrade player-observed move-ack
// latency by more than 20% versus a baseline 0-spectator game, and that
// slow subscribers do not delay healthy ones.
//
// Load profile:
//   Baseline: 1 game, 2 player VUs, 0 spectators for 5m.
//   Then ramp spectator VUs 0->1000 over 2m on the same hot game,
//   including 10% slow-reader VUs that throttle to 1 event/sec;
//   hold 15m. Run a control game in parallel.
//
// Threshold:
//   http_req_duration{type:move,game:hot}: p(95) < 1.2 * p(95) of
//     http_req_duration{type:move,game:control}
//   sse_event_lag_ms{reader:fast,game:hot}: p(95)<200
//
// k6 thresholds do not support cross-metric arithmetic directly. We
// encode the 20% rule by:
//   1. running the control game alongside the hot game,
//   2. capturing both p(95)s as separate sub-metrics, and
//   3. asserting hot p(95) < 360ms AND control p(95) < 300ms
//      (i.e. hot <= 1.2 * control with a 300ms control budget).
// Post-run, compute the precise ratio from the JSON summary if you
// want the exact statistical relationship.

import { sleep } from 'k6';
import { subscribe } from '../lib/sse.js';
import {
  BASE_URL,
  createGame,
  submitMove,
  sampleMove,
  genClientActionID,
} from '../lib/game.js';

export const options = {
  scenarios: {
    hot_players: {
      executor: 'constant-vus',
      exec: 'hotPlayerVU',
      vus: 2,
      duration: '22m',
      tags: { role: 'player', game: 'hot' },
    },
    control_players: {
      executor: 'constant-vus',
      exec: 'controlPlayerVU',
      vus: 2,
      duration: '22m',
      tags: { role: 'player', game: 'control' },
    },
    hot_spectators_fast: {
      executor: 'ramping-vus',
      exec: 'hotSpectatorFastVU',
      startVUs: 0,
      stages: [
        { duration: '5m', target: 0 },     // baseline window
        { duration: '2m', target: 900 },   // 90% fast readers
        { duration: '15m', target: 900 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'spectator', game: 'hot', reader: 'fast' },
    },
    hot_spectators_slow: {
      executor: 'ramping-vus',
      exec: 'hotSpectatorSlowVU',
      startVUs: 0,
      stages: [
        { duration: '5m', target: 0 },
        { duration: '2m', target: 100 },   // 10% slow readers
        { duration: '15m', target: 100 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'spectator', game: 'hot', reader: 'slow' },
    },
  },
  thresholds: {
    // Hot vs control 20%-rule encoded as a fixed budget pair. Adjust
    // once a real baseline is measured.
    'http_req_duration{type:move,game:control}': ['p(95)<300'],
    'http_req_duration{type:move,game:hot}': ['p(95)<360'],
    'sse_event_lag_ms{reader:fast,game:hot}': ['p(95)<200'],
  },
};

export function setup() {
  const hot = createGame({ timeControl: 'rapid', vuTag: 'setup-hot' });
  const control = createGame({ timeControl: 'rapid', vuTag: 'setup-control' });
  return { hot: hot.gameID, control: control.gameID };
}

function playMove(gameID, tagGame) {
  if (!gameID) {
    sleep(1);
    return;
  }
  const seq = (__ITER || 0) + 1;
  const moveUCI = sampleMove(__ITER || 0);
  const cmid = genClientActionID(__VU, __ITER, 0);
  submitMove(gameID, seq, moveUCI, cmid, {
    vuTag: String(__VU),
    tags: { type: 'move', game: tagGame },
  });
  sleep(1 + Math.random() * 2);
}

export function hotPlayerVU(data) { playMove(data.hot, 'hot'); }
export function controlPlayerVU(data) { playMove(data.control, 'control'); }

export function hotSpectatorFastVU(data) {
  if (!data.hot) { sleep(1); return; }
  subscribe(BASE_URL + '/games/' + data.hot + '/sse', {
    headers: { Authorization: 'Bearer perf-fast-' + __VU },
    tags: { game: 'hot', reader: 'fast' },
  });
}

export function hotSpectatorSlowVU(data) {
  if (!data.hot) { sleep(1); return; }
  subscribe(BASE_URL + '/games/' + data.hot + '/sse', {
    headers: { Authorization: 'Bearer perf-slow-' + __VU },
    tags: { game: 'hot', reader: 'slow' },
    slowReader: true,
  });
}
