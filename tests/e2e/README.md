# KISS-Chess E2E Test Scaffold

End-to-end tests for the Go backend at `src/server/` (not yet implemented).

> **Tests are scaffolds.** Every test body begins with
> `t.Skip("scaffold: awaiting src/server implementation")`. They document
> intended behavior and compile as-is. Remove the Skip and wire the helpers
> against the real server once handlers exist.

## Architecture

- Vanilla `testing`, no external deps.
- One `*httptest.Server` per test, wrapping the future `src/server` handler.
- SSE consumed by reading the raw `net/http` response body line-by-line.
- Move POST bodies are hand-rolled JSON matching the `MoveReq` contract
  (`game_id`, `seq`, `move`, `client_action_id`, `claim_draw`; PlayerID resolved
  server-side from auth, never sent by the client).
- Body limit: 512 bytes, `DisallowUnknownFields` — helpers stay minimal.

## How to run

```sh
go test ./tests/e2e/...
```

All tests will print `--- SKIP` until the server is wired.

## Coverage map

| File                    | Test(s)                                          | Spec refs                                                 |
| ----------------------- | ------------------------------------------------ | --------------------------------------------------------- |
| `game_flow_test.go`     | `TestE2E_GameFlow_TwoPlayerCheckmate`            | US1; FR-001, 005, 006, 007, 009, 011, 021, 022, 023       |
| `clock_test.go`         | `TestE2E_FlagOnTime`                             | US2; FR-015..FR-020; SC-005                               |
| `spectator_test.go`     | `TestE2E_SpectatorFanout`                        | US3; FR-021, 022; SC-003                                  |
| `reconnect_test.go`     | `TestE2E_LastEventIDReplay`, `..._SnapshotFallback` | US4; FR-024, 025                                       |
| `concurrency_test.go`   | `TestE2E_StaleSequenceRejected`, `..._IdempotentRetry` | FR-013, 014; SC-006, SC-007                          |

## Helpers (`helpers.go`)

- `startServer(t)` — returns `*httptest.Server` pointing at the future handler.
- `parseSSE(r)` — yields `(id, event, data)` triples from an SSE stream.
- `buildMoveReq(seq, move, clientActionID)` — produces a wire-format `MoveReq` JSON body.
- `randomGameID()` — opaque per-test game id.

## Endpoints under test

- `POST /games/{id}/move` — authoritative move mutation.
- `GET  /games/{id}/sse` — one-way SSE stream; supports `Last-Event-ID`.
- `GET  /games/{id}/snapshot` — recovery / initial load.
