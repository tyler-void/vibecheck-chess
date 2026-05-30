# bare-chess performance tests (k6)

Vanilla k6 ES modules. No Babel, no TypeScript, no bundler. Each scenario
under `scenarios/` is independently runnable with the `k6` CLI.

The server (`src/server/`) does not exist yet. These scripts will RUN today
against any HTTP listener on `BASE_URL`, but most thresholds will fail until
the real endpoints are implemented. They become meaningful once the server
is in place.

## Endpoints under test

- `POST /games/{id}/move`     — authoritative move mutation
- `GET  /games/{id}/sse`      — Server-Sent Events stream (board + clocks)
- `GET  /games/{id}/snapshot` — recovery / initial board snapshot

See the feature spec / plan for the full contract; this harness mirrors the
JSON shapes in `lib/game.js` and `lib/sse.js`.

## Running

```sh
k6 run tests/perf/scenarios/move-throughput.js
k6 run tests/perf/scenarios/capacity-soak.js
k6 run tests/perf/scenarios/hot-match-fanout.js
k6 run tests/perf/scenarios/reconnect-resume.js
k6 run tests/perf/scenarios/flagging-accuracy.js
k6 run tests/perf/scenarios/concurrency-correctness.js
k6 run tests/perf/scenarios/restart-durability.js
```

Override the target via environment variable:

```sh
BASE_URL=http://127.0.0.1:8080 k6 run tests/perf/scenarios/move-throughput.js
BASE_URL=https://chess.example.com k6 run tests/perf/scenarios/capacity-soak.js
```

Default `BASE_URL` is `http://localhost:8080`.

## Scenarios <-> Success Criteria

| File                                       | SC IDs                | What it proves                                                                                       |
| ------------------------------------------ | --------------------- | ---------------------------------------------------------------------------------------------------- |
| `scenarios/move-throughput.js`             | SC-001                | p95 SSE event lag <200ms under 10k VU realistic play (1000 games, 2k players + 8k spectators).       |
| `scenarios/capacity-soak.js`               | SC-002, SC-010        | 10k concurrent SSE for 1h with bounded heap/goroutines; idle streams survive 5min+ proxy timeout.    |
| `scenarios/hot-match-fanout.js`            | SC-003, SC-009        | 1000 spectators on one game add <=20% move-ack latency vs control; slow readers don't block fast.    |
| `scenarios/reconnect-resume.js`            | SC-004                | >=99% of reconnects via `Last-Event-ID` resume with zero gap and <0.1% duplicates.                   |
| `scenarios/flagging-accuracy.js`           | SC-005                | Loss-on-time adjudicated and broadcast within 100ms p95 / 250ms p99 of server-side zero crossing.    |
| `scenarios/concurrency-correctness.js`     | SC-006, SC-007, SC-008| Idempotent retries apply once, racing same-seq submissions yield exactly one commit, no illegal moves.|
| `scenarios/restart-durability.js`          | SC-011                | Graceful restart never silently loses a move; every in-flight move acks or errors retryably.         |

## Threshold semantics

Thresholds use k6's `thresholds` block. The exact expressions are reproduced
verbatim from the spec inputs (e.g. `sse_event_lag_ms{type:move}: p(95)<200`).

- `p(95)<200` means the 95th percentile must be under 200ms.
- `rate<0.001` means fewer than 0.1% of tagged samples may be true.
- `rate==0` means zero tolerance; any occurrence fails the run.

When the server is missing, custom metrics will simply not be emitted (rate
denominator is zero); HTTP-side metrics will still fail loudly so you can
verify the harness is wired.

## Lib layout

- `lib/game.js` — `createGame`, `submitMove`, `getSnapshot`, `parseSnapshot`.
- `lib/sse.js`  — minimal `k6/experimental/sse` consumer; falls back to an
  HTTP streaming approach if the experimental module is not present in your
  k6 build (see comments in that file).

## Caveats

- Game creation is not exposed over HTTP in the reference design (rooms are
  created in-process via `GameManager.Create`). The harness assumes a small
  test-only seeding endpoint at `POST /test/games` returning
  `{ "game_id": "..." }`. If your server uses something different, change
  `createGame()` in `lib/game.js`.
- The `PlayerID` is taken from auth on the server, not the body. Tests send
  a synthetic `Authorization: Bearer perf-vu-<n>` header you can wire to a
  test-mode auth provider.
- `client_action_id` is generated per VU iteration and reused for idempotency
  test retries.
