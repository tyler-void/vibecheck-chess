package chess

import "log/slog"

// LogHooks is the observability seam for structured logging. Like
// MetricsHooks (above), the chess package doesn't import log/slog
// directly — it emits to these callbacks. Production wires them in
// cmd/server/main.go; tests leave them nil.
//
// Phase 9 T086.
type LogHooks struct {
	// ActionAccepted fires once per successful Apply* invocation.
	ActionAccepted func(kind ActionKind, fields ...slog.Attr)

	// TerminalReached fires once when a game transitions from Ongoing to
	// any terminal Outcome. Fired from inside the Apply* path that caused
	// the transition (after lock release, before broadcast). Useful for
	// audit-style logs.
	TerminalReached func(fields ...slog.Attr)
}

// Log is the package-global log hook set.
var Log LogHooks

func logActionAccepted(kind ActionKind, gameID GameID, seq uint64) {
	if Log.ActionAccepted != nil {
		Log.ActionAccepted(kind,
			slog.String("game_id", string(gameID)),
			slog.Uint64("seq", seq),
		)
	}
}

func logTerminal(gameID GameID, outcome Outcome, term TerminationReason, seq uint64) {
	if Log.TerminalReached != nil {
		Log.TerminalReached(
			slog.String("game_id", string(gameID)),
			slog.Int("outcome", int(outcome)),
			slog.Int("termination", int(term)),
			slog.Uint64("seq", seq),
		)
	}
}
