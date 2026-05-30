# Tests

Two-tier test architecture for the bare-chess server.

- **e2e** (`tests/e2e/`): Go integration tests exercising the HTTP + SSE surface against an in-process server handler via `httptest.NewServer`. Targets low-level correctness: move legality, seq monotonicity, idempotency, SSE recovery via Last-Event-ID, clock flag adjudication, lock-ordering invariants.
- **perf** (`tests/perf/`): k6 scenarios driving the public endpoints. Targets capacity (10k CCU on one node) and the spec's Success Criteria (move latency, fan-out isolation, sequence-gap rate, reconnect resume). Requires a custom k6 build with [`phymbert/xk6-sse`](https://github.com/phymbert/xk6-sse) — k6 has no first-party SSE support (`grafana/k6#746`).

> **Scaffolds only.** The server implementation under `cmd/server` + `internal/` is not yet written. Every Go test starts with `t.Skip("scaffold: awaiting impl")` and every k6 scenario will fail thresholds against a non-existent server. Files compile and parse — do not interpret a green run as a passing system. Coverage is wired by file headers (each file lists its FR-### / SC-### / US-#).
>
> **Pivot note (2026-05-30)**: Scaffolds were authored before the Datastar pivot. They reference generic SSE event names (`board`, `snapshot`, `terminal`). When the server lands, the wire-level event names will be `datastar-patch-elements` and `datastar-patch-signals` (see `specs/001-kiss-chess-engine/contracts/sse-events.md`). The k6 fallback path uses `Accept: application/json` so most perf scenarios continue to assert against JSON snapshots; only the SSE parser in `tests/perf/lib/sse.js` and the e2e SSE helper need their event-name expectations adjusted. No structural rewrite required.

## Coverage

### e2e (Go integration)

| User story / requirement                                              | File                                |
| --------------------------------------------------------------------- | ----------------------------------- |
| US-1 two-player game · FR-001/005/006/007/009/011/021/022/023        | `tests/e2e/game_flow_test.go`       |
| US-2 flag on time · FR-015..FR-020 · SC-005                           | `tests/e2e/clock_test.go`           |
| US-3 spectator fan-out · FR-021/022 · SC-003                          | `tests/e2e/spectator_test.go`       |
| US-4 reconnect resume · FR-024/025                                    | `tests/e2e/reconnect_test.go`       |
| Optimistic concurrency + idempotency · FR-013/014 · SC-006/SC-007     | `tests/e2e/concurrency_test.go`     |
| Shared helpers (server boot, SSE parsing, move-req builder)           | `tests/e2e/helpers.go`              |

### perf (k6)

| Success Criteria  | Scenario                                          |
| ----------------- | ------------------------------------------------- |
| SC-001            | `tests/perf/scenarios/move-throughput.js`         |
| SC-002, SC-010    | `tests/perf/scenarios/capacity-soak.js`           |
| SC-003, SC-009    | `tests/perf/scenarios/hot-match-fanout.js`        |
| SC-004            | `tests/perf/scenarios/reconnect-resume.js`        |
| SC-005            | `tests/perf/scenarios/flagging-accuracy.js`       |
| SC-006, SC-007, SC-008 | `tests/perf/scenarios/concurrency-correctness.js` |
| SC-011            | `tests/perf/scenarios/restart-durability.js`      |

## Commands

```sh
# Go integration suite (compiles, all tests Skip until src/server lands)
cd tests/e2e && go test ./...

# k6 performance scenarios — run one at a time, override BASE_URL as needed
BASE_URL=http://localhost:8080 k6 run tests/perf/scenarios/move-throughput.js
BASE_URL=http://localhost:8080 k6 run tests/perf/scenarios/hot-match-fanout.js
BASE_URL=http://localhost:8080 k6 run tests/perf/scenarios/capacity-soak.js
# ...and the rest under tests/perf/scenarios/
```

Perf runs require `ulimit -n 65535` (or higher) on both the SUT host and the k6 host, plus enough ephemeral ports for 10k connections. See each scenario file's header for its load profile and threshold semantics.
