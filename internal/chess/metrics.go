package chess

import "time"

// Metrics callbacks for observability (Phase 9 T107). The chess package is
// transport-agnostic — it doesn't import the Prometheus client. Instead it
// emits to these package-level hooks, which the HTTP layer (or tests) wire
// in at process startup.
//
// Zero values are no-ops, so the chess package is functional without the
// HTTP layer being initialised (e.g., in unit tests).

// ActionKind is the discriminator for kiss_actions_accepted_total{kind=...}.
type ActionKind string

const (
	ActionMove       ActionKind = "move"
	ActionResign     ActionKind = "resign"
	ActionDrawOffer  ActionKind = "draw_offer"
	ActionDrawAccept ActionKind = "draw_accept"
	ActionDrawClaim  ActionKind = "draw_claim"
)

// MoveOutcome is the discriminator for kiss_moves_accepted_total{outcome=...}.
type MoveOutcome string

const (
	MoveOutcomeOK        MoveOutcome = "ok"
	MoveOutcomeStale     MoveOutcome = "stale"
	MoveOutcomeIllegal   MoveOutcome = "illegal"
	MoveOutcomeNotTurn   MoveOutcome = "not_turn"
	MoveOutcomeFlagged   MoveOutcome = "flagged"
	MoveOutcomeBadSyntax MoveOutcome = "bad_syntax"
)

// MetricsHooks is the observability seam between the chess package and
// whatever metric backend the host application uses. Set fields at process
// startup (cmd/server/main.go); leave nil to silence the corresponding
// signal.
type MetricsHooks struct {
	// ActionAccepted is called for every successfully applied state-
	// changing action, AFTER the lock is released and the broadcast has
	// gone out. Duration is the wall-clock time spent inside the lock.
	ActionAccepted func(kind ActionKind, duration time.Duration)

	// MoveOutcome is called for every move attempt, including rejections.
	// Separate from ActionAccepted because moves have a richer outcome
	// taxonomy (illegal, not-turn, etc.) that's worth surfacing as its
	// own counter for fine-grained debugging.
	MoveOutcome func(outcome MoveOutcome)
}

// Metrics is the package-global hook set. Assignment is intended to happen
// exactly once at process startup. Concurrent reads against a constant
// MetricsHooks value are safe.
var Metrics MetricsHooks

func recordAction(kind ActionKind, start time.Time) {
	if Metrics.ActionAccepted != nil {
		Metrics.ActionAccepted(kind, time.Since(start))
	}
}

func recordMoveOutcome(outcome MoveOutcome) {
	if Metrics.MoveOutcome != nil {
		Metrics.MoveOutcome(outcome)
	}
}
