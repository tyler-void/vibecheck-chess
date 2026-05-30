package chess

import (
	"testing"

	libchess "github.com/corentings/chess/v2"
)

// Perft (PERFormance Test) — Stockfish-style legality regression. Each test
// case loads a canonical position by FEN, walks the move tree to a given
// depth, and counts leaf nodes. The expected counts come from the
// chess-programming-wiki Perft Results table; any divergence indicates a
// legality bug somewhere in the move-generation chain.
//
// CI runs depths 1-4 on every PR (fast); depths 5-6 run nightly under
// -short=false. Constitution VI / research R-25.

type perftCase struct {
	name  string
	fen   string
	depth int
	want  uint64
}

// Canonical depths. Listed depths 1-4 for PR; depths 5-6 are guarded by
// !testing.Short().
var perftCases = []perftCase{
	// Starting position.
	{"start_d1", libchessStartingFEN, 1, 20},
	{"start_d2", libchessStartingFEN, 2, 400},
	{"start_d3", libchessStartingFEN, 3, 8902},
	{"start_d4", libchessStartingFEN, 4, 197281},
	// Kiwipete — heavy tactical position.
	{"kiwipete_d1", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 1, 48},
	{"kiwipete_d2", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 2, 2039},
	{"kiwipete_d3", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 3, 97862},
	// Position 3 — endgame with pawn promotion paths.
	{"pos3_d1", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 1, 14},
	{"pos3_d2", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 2, 191},
	{"pos3_d3", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 3, 2812},
	{"pos3_d4", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 4, 43238},
	// Position 4 — complex mid-game with castling rights for both sides.
	{"pos4_d1", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 1, 6},
	{"pos4_d2", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 2, 264},
	// Position 5.
	{"pos5_d1", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", 1, 44},
	{"pos5_d2", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", 2, 1486},
	// Position 6.
	{"pos6_d1", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 1, 46},
}

// Depths 5-6 are slower; gated behind !testing.Short(). Run nightly.
var perftNightlyCases = []perftCase{
	{"start_d5", libchessStartingFEN, 5, 4865609},
	{"start_d6", libchessStartingFEN, 6, 119060324},
	{"kiwipete_d4", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 4, 4085603},
	{"pos3_d5", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 5, 674624},
	{"pos4_d3", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 3, 9467},
}

// libchessStartingFEN is the canonical chess starting position. The "20 legal
// first moves" property (8 pawn × 2 + 4 knight) is the most-cited perft sanity
// check.
const libchessStartingFEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

func TestPerft(t *testing.T) {
	for _, tc := range perftCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := perft(t, tc.fen, tc.depth)
			if got != tc.want {
				t.Errorf("perft(%s, depth=%d) = %d, want %d", tc.name, tc.depth, got, tc.want)
			}
		})
	}
}

func TestPerft_Nightly(t *testing.T) {
	if testing.Short() {
		t.Skip("nightly perft (depths 5-6) skipped under -short")
	}
	for _, tc := range perftNightlyCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := perft(t, tc.fen, tc.depth)
			if got != tc.want {
				t.Errorf("perft(%s, depth=%d) = %d, want %d", tc.name, tc.depth, got, tc.want)
			}
		})
	}
}

// perft counts the number of leaf positions at the given depth from the
// position encoded by fen.
func perft(t *testing.T, fen string, depth int) uint64 {
	t.Helper()
	opt, err := libchess.FEN(fen)
	if err != nil {
		t.Fatalf("FEN %q: %v", fen, err)
	}
	g := libchess.NewGame(opt)
	return perftRec(g, depth)
}

func perftRec(g *libchess.Game, depth int) uint64 {
	if depth == 0 {
		return 1
	}
	moves := g.ValidMoves()
	if depth == 1 {
		return uint64(len(moves))
	}
	var count uint64
	for i := range moves {
		clone := g.Clone()
		// Apply by index via the library's unsafe path — these are
		// pre-validated by ValidMoves so unsafe is correct here.
		if err := clone.UnsafeMove(&moves[i], nil); err != nil {
			continue
		}
		count += perftRec(clone, depth-1)
	}
	return count
}
