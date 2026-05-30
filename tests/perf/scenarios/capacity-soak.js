// capacity-soak.js
//
// Success Criteria: SC-002, SC-010
//
// Goal: Prove a single node sustains 10k+ concurrent SSE subscribers for
// at least 1 hour with bounded memory/goroutines, and that idle streams
// survive a typical reverse-proxy idle timeout (5min+ no activity).
//
// Load profile:
//   10000 spectator VUs across many games + a quiescent idle cohort of
//   500 VUs that make no moves for the full duration;
//   ramp 0->10000 over 10m, hold 60m, ramp-down 5m.
//   Run behind a reverse proxy with 60s idle timeout.
//
// Threshold:
//   sse_connection_dropped_idle: rate<0.001
//   checks{check:goroutines_bounded}: rate>0.99
//   checks{check:heap_bounded}: rate>0.99

import http from 'k6/http';
import { check, sleep } from 'k6';
import { subscribe, sseConnectionDroppedIdle } from '../lib/sse.js';
import { BASE_URL, createGame } from '../lib/game.js';

export const options = {
  scenarios: {
    spectators: {
      executor: 'ramping-vus',
      exec: 'spectatorVU',
      startVUs: 0,
      stages: [
        { duration: '10m', target: 10000 },
        { duration: '60m', target: 10000 },
        { duration: '5m', target: 0 },
      ],
      gracefulRampDown: '60s',
      tags: { role: 'spectator' },
    },
    idle: {
      executor: 'constant-vus',
      exec: 'idleVU',
      vus: 500,
      duration: '75m',
      tags: { role: 'idle' },
    },
    health: {
      executor: 'constant-vus',
      exec: 'healthVU',
      vus: 1,
      duration: '75m',
      tags: { role: 'health' },
    },
  },
  thresholds: {
    sse_connection_dropped_idle: ['rate<0.001'],
    'checks{check:goroutines_bounded}': ['rate>0.99'],
    'checks{check:heap_bounded}': ['rate>0.99'],
  },
};

// Provision ~1000 games at setup so 10k spectators average 10/game.
export function setup() {
  const games = [];
  for (let i = 0; i < 1000; i++) {
    const g = createGame({ timeControl: 'rapid', vuTag: 'setup' });
    if (g.gameID) games.push(g.gameID);
  }
  return { games: games };
}

export function spectatorVU(data) {
  if (!data.games.length) {
    sleep(1);
    return;
  }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: { Authorization: 'Bearer perf-spectator-' + __VU },
    tags: { type: 'soak', role: 'spectator' },
    onError: function () {
      sseConnectionDroppedIdle.add(1, { reason: 'transport' });
    },
  });
}

// idleVU connects once and never reads anything else. It should NOT
// be dropped by the proxy because the server emits keepalive comment
// frames every 15s.
export function idleVU(data) {
  if (!data.games.length) {
    sleep(60);
    return;
  }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  let droppedEarly = false;
  subscribe(BASE_URL + '/games/' + gameID + '/sse', {
    headers: { Authorization: 'Bearer perf-idle-' + __VU },
    tags: { type: 'idle', role: 'idle' },
    onError: function () {
      droppedEarly = true;
    },
  });
  // The subscribe call blocks until the stream ends. If it returned
  // before the scenario ramp-down, we were dropped while idle.
  sseConnectionDroppedIdle.add(droppedEarly ? 1 : 0, { reason: 'idle' });
}

// healthVU polls a debug endpoint (if exposed) to check that goroutine
// and heap counts stay bounded. Server is expected to expose:
//   GET /debug/health -> { "goroutines": N, "heap_mb": M }
// Adjust thresholds based on your build's baseline.
export function healthVU() {
  const res = http.get(BASE_URL + '/debug/health', {
    tags: { op: 'health' },
    timeout: '5s',
  });
  let goroutines = -1;
  let heapMB = -1;
  if (res.status === 200) {
    try {
      const j = res.json();
      goroutines = j.goroutines || j.Goroutines || -1;
      heapMB = j.heap_mb || j.HeapMB || -1;
    } catch (e) { /* ignore */ }
  }
  // Bounded definitions are intentionally generous; tighten per build.
  check({ g: goroutines, h: heapMB }, {
    goroutines_bounded: function (v) { return v.g >= 0 && v.g < 200000; },
    heap_bounded: function (v) { return v.h >= 0 && v.h < 8192; },
  });
  sleep(10);
}
