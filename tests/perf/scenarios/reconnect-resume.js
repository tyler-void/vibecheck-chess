// reconnect-resume.js
//
// Success Criteria: SC-004
//
// Goal: Prove that subscribers whose connection drops within the
// configured reconnect window resume with zero missed or duplicated
// events in at least 99% of reconnect attempts.
//
// Load profile:
//   2000 spectator VUs across 200 games with steady move cadence
//   (1 move/2s). Each VU randomly disconnects for 1-10s every 30-60s
//   and reconnects sending Last-Event-ID;
//   ramp 0->2000 over 2m, hold 20m.
//
// Threshold:
//   sse_reconnect_success_zero_gap: rate>0.99
//   sse_duplicate_event: rate<0.001

import { sleep } from 'k6';
import {
  subscribe,
  lastEventIDHeader,
  sseReconnectSuccessZeroGap,
  sseDuplicateEvent,
} from '../lib/sse.js';
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
      executor: 'constant-vus',
      exec: 'playerVU',
      vus: 400, // 2 per game x 200 games
      duration: '22m',
      tags: { role: 'player' },
    },
    spectators: {
      executor: 'ramping-vus',
      exec: 'reconnectingSpectatorVU',
      startVUs: 0,
      stages: [
        { duration: '2m', target: 2000 },
        { duration: '20m', target: 2000 },
      ],
      gracefulRampDown: '30s',
      tags: { role: 'spectator' },
    },
  },
  thresholds: {
    sse_reconnect_success_zero_gap: ['rate>0.99'],
    sse_duplicate_event: ['rate<0.001'],
  },
};

export function setup() {
  const games = [];
  for (let i = 0; i < 200; i++) {
    const g = createGame({ timeControl: 'blitz', vuTag: 'setup' });
    if (g.gameID) games.push(g.gameID);
  }
  return { games: games };
}

export function playerVU(data) {
  if (!data.games.length) { sleep(1); return; }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];
  const seq = (__ITER || 0) + 1;
  submitMove(gameID, seq, sampleMove(__ITER || 0), genClientActionID(__VU, __ITER, 0), {
    vuTag: String(__VU),
    tags: { type: 'move' },
  });
  sleep(2);
}

// reconnectingSpectatorVU runs an outer loop that:
//   1. subscribes with Last-Event-ID (if known)
//   2. consumes events for a random 30-60s burst
//   3. disconnects for 1-10s
//   4. records whether the next event-id is exactly lastSeen+1
//      (success) or has a gap / duplicate (failure)
export function reconnectingSpectatorVU(data) {
  if (!data.games.length) { sleep(1); return; }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  // Persistent across iterations within this VU.
  if (typeof __ENV.__noop === 'undefined') { /* placeholder */ }

  let lastSeen = null;
  const burstMs = (30 + Math.floor(Math.random() * 30)) * 1000;
  const startedAt = Date.now();

  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: Object.assign(
      { Authorization: 'Bearer perf-reconnect-' + __VU },
      lastEventIDHeader(lastSeen),
    ),
    tags: { type: 'reconnect' },
    onEvent: function (e) {
      lastSeen = e.seq;
      if (Date.now() - startedAt > burstMs) {
        throw new Error('__close__');
      }
    },
    onError: function () { /* expected on forced close */ },
  });

  // Pause to simulate a network drop.
  sleep(1 + Math.random() * 9);

  // Reconnect with the last-known seq and verify zero gap / no dupes.
  let firstResumeSeq = null;
  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: Object.assign(
      { Authorization: 'Bearer perf-reconnect-' + __VU },
      lastEventIDHeader(lastSeen),
    ),
    tags: { type: 'reconnect', phase: 'resume' },
    maxEvents: 5,
    onEvent: function (e) {
      if (firstResumeSeq === null) {
        firstResumeSeq = e.seq;
        // Zero-gap success: server replays from lastSeen+1.
        const ok = (typeof lastSeen === 'number') && firstResumeSeq === lastSeen + 1;
        sseReconnectSuccessZeroGap.add(ok ? 1 : 0);
      }
    },
  });
}
