// move-throughput.js
//
// Success Criteria: SC-001
//
// Goal: Prove that under sustained realistic play, at least 95% of accepted
// moves are delivered to opponents and in-game spectators within 200ms of
// server acceptance.
//
// Load profile:
//   1000 games, 2000 player VUs + 8000 spectator VUs;
//   ramp 0->10000 VUs over 5m, hold 30m, ramp-down 2m.
//   Each game sustains a move every 1-5s.
//
// Threshold:
//   sse_event_lag_ms{type:move}: p(95)<200

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
    players: {
      executor: 'ramping-vus',
      exec: 'playerVU',
      startVUs: 0,
      stages: [
        { duration: '5m', target: 2000 },
        { duration: '30m', target: 2000 },
        { duration: '2m', target: 0 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'player' },
    },
    spectators: {
      executor: 'ramping-vus',
      exec: 'spectatorVU',
      startVUs: 0,
      stages: [
        { duration: '5m', target: 8000 },
        { duration: '30m', target: 8000 },
        { duration: '2m', target: 0 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'spectator' },
    },
  },
  thresholds: {
    'sse_event_lag_ms{type:move}': ['p(95)<200'],
  },
  // Avoid global timeouts on long SSE streams; per-request timeouts
  // are set explicitly where needed.
  noConnectionReuse: false,
};

// Shared pool of game IDs. We provision 1000 games at setup() and
// distribute work across them via deterministic indexing on __VU.
export function setup() {
  const games = [];
  for (let i = 0; i < 1000; i++) {
    const g = createGame({ timeControl: 'blitz', vuTag: 'setup' });
    if (g.gameID) games.push(g.gameID);
  }
  // If the server isn't running yet, games may be empty. Scenarios
  // still execute and HTTP failures will surface in metrics.
  return { games: games };
}

export function playerVU(data) {
  if (!data.games.length) {
    sleep(1);
    return;
  }
  // 2 players per game across 2000 player VUs -> 1000 games.
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  // Each iteration submits one move at a 1-5s cadence.
  const seq = (__ITER || 0) + 1;
  const moveUCI = sampleMove(__ITER || 0);
  const cmid = genClientActionID(__VU, __ITER, 0);
  submitMove(gameID, seq, moveUCI, cmid, {
    vuTag: String(__VU),
    tags: { type: 'move', role: 'player' },
  });
  sleep(1 + Math.random() * 4);
}

export function spectatorVU(data) {
  if (!data.games.length) {
    sleep(1);
    return;
  }
  // 8 spectators per game on average.
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: { Authorization: 'Bearer perf-spectator-' + __VU },
    tags: { type: 'move', role: 'spectator' },
    onEvent: function () { /* lag is recorded in lib/sse.js */ },
  });
}
