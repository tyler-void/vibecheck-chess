package chess

import (
	libchess "github.com/corentings/chess/v2"
)

// uciEncodeMove returns the UCI string for a *libchess.Move via the
// library's canonical encoder. Used by the rapid PBT to drive ApplyMove
// through the same wire-input path the real HTTP handler exercises.
//
// Defined here (not state_machine_test.go) so it's available outside the
// test build — handy if a future caller wants to log moves in UCI form.
func uciEncodeMove(mv interface{}) string {
	if m, ok := mv.(*libchess.Move); ok && m != nil {
		return libchess.UCINotation{}.Encode(nil, m)
	}
	return ""
}
