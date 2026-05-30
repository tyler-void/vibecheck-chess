# Changelog

Public timeline of changes to bare-chess.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Dates are ISO-8601 in the project's local timezone.

This file is **public** when the repo is published (`PUBLIC_RELEASE.md`). The spec artifacts under `specs/`, `references/`, `CLAUDE.md`, `.claude/`, and `.specify/` remain **private** — this changelog records *what happened and when*, not *what was decided in the spec*. A reader of the public repo can trace the project's evolution from this file plus git history; they cannot reconstruct the proprietary design process.

## How entries are added

After any non-trivial change (spec authored, plan iterated, architecture pivot, feature implemented, library swap, perf milestone hit), append a new entry at the top of `## Unreleased`. Each entry is one line: a verb + an object + (optionally) a one-clause "why". Examples:

- `Authored initial feature spec — KISS-Chess realtime engine (35 FRs, 11 SCs).`
- `Pivoted client framework from Alpine.js to Datastar for SSE-first reactivity.`
- `Shipped FanoutHub with bounded non-blocking per-subscriber queues.`

When a public release is cut, rename `## Unreleased` to `## [vX.Y.Z] — YYYY-MM-DD` and add a fresh `## Unreleased` above it. Pre-v1, "release" means a git tag; post-v1, it means a versioned binary.

Do **not** paste spec text, code blocks, or planning rationale into entries. The point of this file is the *outside* view.

---

## Unreleased

### 2026-05-31

- Shipped Phase 3 (MVP core — US1) end-to-end. Two browsers can now create a game, alternate UCI moves, watch their opponent's moves arrive via Datastar-format SSE, and terminate by checkmate. Per-rule legality tests landed (castling, en passant, promotion, checkmate, stalemate, insufficient material). Perft canonical positions clean at depths 1–4 on every PR (depths 5–6 nightly). FuzzLegality runs 1.6M differential checks against `notnil/chess` in 15s with zero divergences. ApplyMove honours parse-outside-lock / mutate / unlock / broadcast per Constitution III; idempotent retries return the cached result with a new `ActionResult.Idempotent` flag so the HTTP layer can distinguish 204 vs 200. Addendum FR-036 / FR-037 shipped alongside: `ApplyResign`, `ApplyDrawOffer`, `ApplyDrawAccept`, three new endpoints, draw-offer banner in `web/game.html`. e2e test scaffolds activated against the real server (merged tests/e2e module into the main kisschess module).
- Shipped Phase 2 (foundational packages) — 16 tasks, 17 Go files, 1,474 LOC. `internal/chess` (primitive types, typed errors, TimeControl, ClockState with monotonic-time debit + Bronstein delay, generic bounded `Ring[T]` with explicit gap reporting for Last-Event-ID replay, BoardSnapshot, GameManager + room stub). `internal/dsenc` (Datastar wire-format encoder; sync.Pool'd buffers; no SDK dependency). `internal/sse` (bounded non-blocking FanoutHub with slow-reader drop policy). `internal/http` (server skeleton with /healthz/readyz/metrics + /static/). `cmd/server` (signal-driven graceful drain). All packages pass `go test -race ./...`; `go vet ./...` clean. End-to-end smoke verified: binary boots, health endpoints respond, static CSS serves, SIGINT triggers graceful shutdown with logged drain.
- Shipped Phase 1 (project setup) — initialised Go module (`kisschess`, go 1.24+; toolchain resolved to 1.25 via a transitive dep), pinned production deps (corentings/chess/v2 v2.3.11, starfederation/datastar-go v1.2.1, prometheus/client_golang v1.23.2) and test deps (notnil/chess v1.10.0, pgregory.net/rapid v1.3.0), vendored Datastar v1.2.1 + cm-chessboard v8.7.0 under `web/vendor/`, downloaded the Tailwind v4 standalone CLI into `bin/`, authored `web/input.css` (v4 CSS-native `@theme`; the planned `tailwind.config.js` was dropped because v4 obsoletes the JS config), compiled `web/output.css` (11.4 KB minified), authored `Makefile` + `.github/workflows/ci.yml` + an expanded `.gitignore`. Deleted the pre-existing empty `src/server/` and `src/client/` directories.
- Tail of the Great Rename — renamed `MoveResult` → `ActionResult` across data-model, tasks, research, and the e2e concurrency scaffold; aligns the Go return-type with the `seenActions` cache it lives in. Reworded SC-003, SC-011, and the matching US3 acceptance scenario from "move" to "action" so latency, durability, and degradation guarantees apply to resignations and draw actions with the same strictness as moves. Tightened T085 / T107 split: T085 strictly declares Prometheus variables and registers them; T107 strictly wires `.Inc()` / `.Observe()` call sites — no overlap.
- Executed the "Great Rename" — standardised the state-mutating action idempotency key across all artifacts: `client_move_id` → `client_action_id`, `MoveReq.ClientMoveID` → `ClientActionID`, and `seenMoves` → `seenActions`. Touches the wire-level field name, the Go struct field, the in-room cache, the spec and contracts, the research notes, and the e2e + perf test scaffolds. Reason: the cache and field were already shared across moves, resignations, and draw actions — the moves-only naming was sloppy and would have confused readers of the Go structs.
- Rewrote SC-001, SC-006, SC-007 to use "action" instead of "move" — explicit coverage of resign + draw under the same 200 ms latency and exactly-once-per-seq guarantees as moves.
- Added the canonical `kiss_actions_accepted_total{kind=move|resign|draw_offer|draw_accept}` Prometheus counter and the `kiss_apply_action_duration_seconds` histogram (replacing the move-specific histogram). The move-outcome breakdown counter is retained for fine-grained move debugging. New Polish task T107 wires the increments at every successful state mutation.
- Aligned the JSON-fallback idempotent-retry shape on `POST /games/{id}/move` with `POST /games/{id}/resign` — both now document `{"result":"ok","seq":<post-action-seq>}` on retry with `Accept: application/json`.

### 2026-05-30

- Added voluntary game termination — resignation (FR-036) and draw offer / accept (FR-037) — to the engine's scope. Three new endpoints (`POST /games/{id}/resign`, `POST /games/{id}/draw-offer`, `POST /games/{id}/draw-offer/accept`); `draw_offer_from` signal projected on the SSE stream so the UI surfaces a pending offer without DOM swaps. Decline is implicit (any subsequent action by the offered-to side withdraws). Six new tasks (T101–T106) under the existing MVP user story.
- Tightened spec wording on flag-on-time latency bound (cites the 100 ms criterion directly) and on slow-subscriber drop policy (cites the 16-event bound directly). Removed a stale board-delta field from the public snapshot example. Added a unit-test case for non-numeric `Last-Event-ID` resume input. Documented the nginx proxy-survival check as a manual pre-release smoke test (not a CI gate).
- Ratified the project constitution at v1.0.0 — seven core principles (KISS, in-memory authoritative state, lock-order discipline, bounded non-blocking fan-out, render-once broadcast-same-bytes, test-first for game rules + concurrency, public timeline / private process), binding technology stack, CI gates, and amendment procedure.
- Pivoted client framework from "Alpine.js + JSON deltas" to **Datastar v1.0+** with the official Go SDK (`github.com/starfederation/datastar-go/datastar`) — SSE-first hypermedia, fine-grained signal reactivity, server-rendered HTML fragments. cm-chessboard retained as an island driven via `data-effect` on a FEN signal.
- Updated plan, research, and contracts to reflect the Datastar wire format (`datastar-patch-elements`, `datastar-patch-signals`) and bumped server Go version requirement from 1.22+ to 1.24+ (Datastar SDK constraint).
- Added testing-pattern research from real flagships: scalachess per-rule test file split (Lichess), Stockfish perft canonical positions, `pgregory.net/rapid` state-machine PBT, `xk6-sse` for SSE load tests, Toxiproxy for chaos injection, differential legality fuzzing against `notnil/chess` or `dragontoothmg`.
- Added this public CHANGELOG and enforced its maintenance via CLAUDE.md.

### 2026-05-30

- Authored implementation plan (Phase 0 research + Phase 1 design + contracts + quickstart) for the KISS-Chess engine. Initial direction: Go + net/http server, Alpine.js + Tailwind client, cm-chessboard, JSON SSE deltas. *(Superseded same day by the Datastar pivot above.)*
- Authored e2e + k6 test scaffolds aligned to spec FR-### / SC-### / US-# identifiers — 1,865 lines across 19 files under `tests/`. All e2e tests are `t.Skip("scaffold: awaiting impl")` until `cmd/server` + `internal/` land.
- Authored `PUBLIC_RELEASE.md` — checklist for the future repo-going-public process (private artifacts to scrub, two release-path options, verification step).
- Authored initial feature specification for the KISS-Chess realtime game engine — 35 functional requirements, 11 measurable success criteria, 6 prioritized user stories, target 10,000+ concurrent SSE subscribers on a single node.
- Created feature branch `001-kiss-chess-engine`.
