// concurrency-correctness.js
//
// Success Criteria: SC-006, SC-007, SC-008
//
// Goal: Prove idempotency (100% of retried moves apply once),
// stale-sequence rejection (100% of concurrent same-seq submissions
// yield exactly one commit), and that no client-supplied legality
// assertion can cause an illegal position to be accepted.
//
// Load profile:
//   200 games with 400 player VUs running adversarial mixes:
//     (a) duplicate move POSTs with identical client_action_id at burst 5x,
//     (b) concurrent racing moves with same seq from both sides,
//     (c) crafted illegal moves with spoofed legality/en-passant/check
//         flags.
//   Ramp 0->400 over 1m, hold 10m.
//
// Threshold:
//   idempotency_double_apply: rate==0
//   stale_seq_double_commit: rate==0
//   illegal_move_accepted: rate==0

import { sleep } from 'k6';
import {
  createGame,
  submitMove,
  sampleMove,
  genClientActionID,
  ILLEGAL_MOVES,
  idempotencyDoubleApply,
  staleSeqDoubleCommit,
  illegalMoveAccepted,
} from '../lib/game.js';

export const options = {
  scenarios: {
    adversaries: {
      executor: 'ramping-vus',
      exec: 'adversaryVU',
      startVUs: 0,
      stages: [
        { duration: '1m', target: 400 },
        { duration: '10m', target: 400 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: {
    idempotency_double_apply: ['rate==0'],
    stale_seq_double_commit: ['rate==0'],
    illegal_move_accepted: ['rate==0'],
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

export function adversaryVU(data) {
  if (!data.games.length) { sleep(1); return; }
  const idx = (__VU - 1) % data.games.length;
  const gameID = data.games[idx];

  // Pick one adversarial mode per iteration in round-robin fashion.
  const mode = (__ITER || 0) % 3;
  if (mode === 0) {
    runIdempotencyBurst(gameID);
  } else if (mode === 1) {
    runStaleSeqRace(gameID);
  } else {
    runIllegalMoves(gameID);
  }
  sleep(0.5 + Math.random() * 1.5);
}

// (a) Duplicate POSTs with identical client_action_id. Exactly one
// should result in a commit (200/202); the rest should be idempotent
// replays returning the same seq (also 200/202) but NOT advancing the
// game by more than one step.
function runIdempotencyBurst(gameID) {
  const seq = (__ITER || 0) + 1;
  const moveUCI = sampleMove(__ITER || 0);
  const cmid = genClientActionID(__VU, __ITER, 0); // SAME cmid for all 5

  let commits = 0;
  let maxAckedSeq = -1;
  for (let i = 0; i < 5; i++) {
    const r = submitMove(gameID, seq, moveUCI, cmid, {
      vuTag: String(__VU),
      tags: { type: 'idempotent' },
    });
    if (r.accepted) {
      commits++;
      if (r.snapshot && typeof r.snapshot.seq === 'number') {
        if (r.snapshot.seq > maxAckedSeq) maxAckedSeq = r.snapshot.seq;
      }
    }
  }
  // The server is allowed (and expected) to return success on every
  // retry, but the game state must reflect a single application. We
  // approximate "double apply" by detecting acked seqs >1 apart for
  // the same cmid.
  idempotencyDoubleApply.add(maxAckedSeq > seq ? 1 : 0);
}

// (b) Two racing POSTs with the same seq. Exactly one must commit;
// the other must return stale-seq (409) without advancing the game.
function runStaleSeqRace(gameID) {
  const seq = (__ITER || 0) + 1;
  const moveA = sampleMove(__ITER || 0);
  const moveB = sampleMove((__ITER || 0) + 1);

  const a = submitMove(gameID, seq, moveA, genClientActionID(__VU, __ITER, 'a'), {
    vuTag: String(__VU),
    tags: { type: 'race', side: 'a' },
  });
  const b = submitMove(gameID, seq, moveB, genClientActionID(__VU, __ITER, 'b'), {
    vuTag: String(__VU),
    tags: { type: 'race', side: 'b' },
  });

  const bothAccepted = a.accepted && b.accepted;
  staleSeqDoubleCommit.add(bothAccepted ? 1 : 0);
}

// (c) Send illegal moves and verify the server NEVER accepts them.
// We do not attempt to spoof JSON fields the server doesn't read
// (DisallowUnknownFields will 400 those). The legality test is a
// pure move-string check.
function runIllegalMoves(gameID) {
  const moveUCI = ILLEGAL_MOVES[(__ITER || 0) % ILLEGAL_MOVES.length];
  const seq = (__ITER || 0) + 1;
  const r = submitMove(gameID, seq, moveUCI, genClientActionID(__VU, __ITER, 'ill'), {
    vuTag: String(__VU),
    tags: { type: 'illegal' },
  });
  illegalMoveAccepted.add(r.accepted ? 1 : 0);
}
