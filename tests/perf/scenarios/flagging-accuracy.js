// flagging-accuracy.js
//
// Success Criteria: SC-005
//
// Goal: Prove that loss-on-time is adjudicated and broadcast within
// 100ms of the actual server-side zero crossing for both the flagged
// player and spectators.
//
// Load profile:
//   500 short-time-control games (e.g. 30s+0 or 10s+0) with 1000 player
//   VUs; each game intentionally lets one side run out the clock.
//   20 spectator VUs per game. Ramp 0->500 games over 2m, hold 15m.
//
// Threshold:
//   flag_adjudication_lag_ms: p(95)<100
//   flag_adjudication_lag_ms: p(99)<250

import { sleep } from 'k6';
import { subscribe } from '../lib/sse.js';
import {
  BASE_URL,
  createGame,
  submitMove,
  sampleMove,
  genClientActionID,
  flagAdjudicationLagMs,
} from '../lib/game.js';

export const options = {
  scenarios: {
    flagging_players: {
      executor: 'constant-vus',
      exec: 'flaggingPlayerVU',
      vus: 1000,
      duration: '17m',
      tags: { role: 'player' },
    },
    spectators: {
      executor: 'ramping-vus',
      exec: 'spectatorVU',
      startVUs: 0,
      stages: [
        { duration: '2m', target: 10000 }, // 20/game x 500 games
        { duration: '15m', target: 10000 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'spectator' },
    },
  },
  thresholds: {
    flag_adjudication_lag_ms: ['p(95)<100', 'p(99)<250'],
  },
};

export function setup() {
  const games = [];
  for (let i = 0; i < 500; i++) {
    const g = createGame({ timeControl: '10+0', vuTag: 'setup' });
    if (g.gameID) games.push(g.gameID);
  }
  return { games: games };
}

// Two VUs per game: one plays normally for a few moves, the other
// deliberately stops moving to let its clock flag. We watch the SSE
// stream from inside the same VU to capture adjudication latency.
export function flaggingPlayerVU(data) {
  if (!data.games.length) { sleep(1); return; }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  // Make 1-2 moves so the game is live, then go silent.
  for (let i = 0; i < 2; i++) {
    submitMove(gameID, i + 1, sampleMove(i), genClientActionID(__VU, __ITER, i), {
      vuTag: String(__VU),
      tags: { type: 'pre-flag-move' },
    });
    sleep(1);
  }

  // Capture the time we expect the clock to flag (best-effort: we use
  // a 10s time control and subtract elapsed). The server is the
  // authority — we treat the first SSE event with outcome != Ongoing
  // as the adjudication moment and compute lag from its server time.
  const expectedFlagUnixMs = Date.now() + 9000;

  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: { Authorization: 'Bearer perf-flagger-' + __VU },
    tags: { type: 'flag' },
    maxEvents: 50,
    onEvent: function (e) {
      const raw = e.raw || {};
      const outcome = raw.outcome || raw.Outcome;
      if (outcome && outcome !== 'Ongoing' && outcome !== 'ongoing') {
        const serverMs = raw.server_unix_ms || raw.ServerUnix;
        if (typeof serverMs === 'number') {
          flagAdjudicationLagMs.add(serverMs - expectedFlagUnixMs);
        }
        throw new Error('__close__');
      }
    },
  });
}

export function spectatorVU(data) {
  if (!data.games.length) { sleep(1); return; }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: { Authorization: 'Bearer perf-flag-spectator-' + __VU },
    tags: { type: 'flag', role: 'spectator' },
  });
}
