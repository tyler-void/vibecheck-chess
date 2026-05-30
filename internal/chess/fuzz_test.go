package chess

import (
	"strings"
	"testing"

	libchess "github.com/corentings/chess/v2"
	notnil "github.com/notnil/chess"
)

// FuzzLegality runs each UCI move through BOTH libraries — corentings/chess/v2
// (production) AND notnil/chess (test-only differential reference). If the
// two disagree on a move's legality, or on the resulting FEN, that's a bug
// in our integration with the production library and must fail the test.
//
// SC-008 / Constitution VI / research R-26. No public Go chess fuzzer exists;
// this is the project's original-work contribution.
//
// Seed corpus: a handful of openings, edge-case moves (en passant,
// promotion, castling), and known illegal moves. The native go-fuzz engine
// generates mutated inputs against this corpus.
func FuzzLegality(f *testing.F) {
	// Seed: opening moves + edge cases (one space-separated UCI sequence per
	// seed). The fuzzer's input is a single string; we split on whitespace
	// and feed each token in sequence to both engines.
	seeds := []string{
		"e2e4",
		"e2e4 e7e5",
		"e2e4 e7e5 g1f3 b8c6 f1c4 f8c5",
		// Fool's mate.
		"f2f3 e7e5 g2g4 d8h4",
		// Promotion run (white a-pawn).
		"a2a4 h7h6 a4a5 h6h5 a5a6 h5h4 a6b7 h4h3",
		// En-passant prep.
		"e2e4 a7a6 e4e5 d7d5 e5d6",
		// Castling prep + execution.
		"e2e4 e7e5 g1f3 g8f6 f1e2 f8e7 e1g1",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Defensively cap input length — the fuzzer would otherwise feed
		// us multi-MB strings and we'd spend all our budget on parsing.
		if len(raw) > 256 {
			raw = raw[:256]
		}

		// Both engines start from the standard initial position. notnil
		// defaults to algebraic notation; force it into UCI mode so
		// MoveStr accepts the same input format as our wire contract.
		ours := libchess.NewGame()
		theirs := notnil.NewGame(notnil.UseNotation(notnil.UCINotation{}))

		tokens := strings.Fields(raw)
		for i, tok := range tokens {
			// Pre-filter via our regex — the fuzzer will produce all sorts
			// of garbage; only UCI-shaped strings are meaningful inputs.
			if err := ValidateUCISyntax(tok); err != nil {
				continue
			}

			ourErr := ours.PushNotationMove(tok, libchess.UCINotation{}, nil)

			// notnil/chess uses its own UCI notation type.
			theirErr := theirs.MoveStr(tok)

			// Both engines must agree on legality.
			ourOK := ourErr == nil
			theirOK := theirErr == nil
			if ourOK != theirOK {
				t.Fatalf("legality divergence at token %d %q: corentings ok=%v (err=%v), notnil ok=%v (err=%v); prior tokens=%v",
					i, tok, ourOK, ourErr, theirOK, theirErr, tokens[:i])
			}
			if !ourOK {
				// Both rejected — both stop progressing through tokens.
				break
			}

			// Both accepted — the resulting FENs must agree on piece
			// placement at minimum. We compare just the first FEN field
			// (board placement) because the two libraries may differ in
			// halfmove-clock or fullmove-number tracking on edge cases,
			// which is implementation-detail noise, not a legality bug.
			ourFEN := firstFENField(ours.FEN())
			theirFEN := firstFENField(theirs.FEN())
			if ourFEN != theirFEN {
				t.Fatalf("FEN divergence after token %d %q:\n  corentings board: %s\n  notnil    board: %s\n  prior tokens: %v",
					i, tok, ourFEN, theirFEN, tokens[:i])
			}
		}
	})
}

// firstFENField returns the piece-placement field of a FEN string (the part
// before the first space). Avoiding strings.Split allocation in the hot path.
func firstFENField(fen string) string {
	if i := strings.IndexByte(fen, ' '); i > 0 {
		return fen[:i]
	}
	return fen
}
