package chess

import "regexp"

// uciRegex captures the strict UCI long-algebraic move syntax we accept on
// the wire: `^[a-h][1-8][a-h][1-8][qrbn]?$`. FR-005.
//
// This is intentionally stricter than the corentings/chess UCINotation decoder
// — we want the regex to be the FIRST gate on untrusted input, before any
// position-aware parsing runs. A bad move shape MUST never reach the chess
// library on the hot path; that's both a correctness rule (FR-008: don't
// trust the client) and a small performance win.
var uciRegex = regexp.MustCompile(`^[a-h][1-8][a-h][1-8][qrbn]?$`)

// ValidateUCISyntax returns nil iff `s` matches the UCI move regex. It does
// NOT check whether the move is legal in the current position — that's the
// chess engine's job after the room lock is taken. The HTTP layer calls this
// BEFORE acquiring the lock so syntactically invalid input is rejected with
// ErrBadMoveSyntax and never enters the mutation path.
func ValidateUCISyntax(s string) error {
	if !uciRegex.MatchString(s) {
		return ErrBadMoveSyntax
	}
	return nil
}
