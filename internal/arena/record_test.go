package arena

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// keySet returns the sorted, dotted key paths of a JSON object (one level of
// nesting into objects and the first element of arrays of objects).
func keySet(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			keys = append(keys, prefix+k)
			switch x := v.(type) {
			case map[string]any:
				walk(prefix+k+".", x)
			case []any:
				if len(x) > 0 {
					if o, ok := x[0].(map[string]any); ok {
						walk(prefix+k+"[].", o)
					}
				}
			}
		}
	}
	walk("", m)
	sort.Strings(keys)
	return strings.Join(keys, "\n") + "\n"
}

func sampleDecision() DecisionRecord {
	return DecisionRecord{
		RunID: "r1", GameID: "g001", ServerGameID: "abc", Seat: "w", Player: "haiku", Model: "claude-haiku-5-5",
		Effort: "default", PromptPack: "chess/v1", ViewSeq: 4, Ply: 4, Mode: "normal", Action: "e2e4", Talk: "hi", MemoLen: 3,
		Attempts: 2, IllegalAttempts: 1, MaxTokensStops: 0, Timeouts: 0, ProviderRetries: 0, StaleResends: 0, ServerRetries: 0,
		AttemptLog: []AttemptLog{{Kind: "illegal", Code: "illegal_move", Action: "e2e5", StopReason: "tool_use",
			LatencyMS: 900, Tokens: Usage{1, 2, 3, 4}, USD: 0.001, ResponseModel: "claude-haiku-5-5"}},
		Tokens: Usage{10, 20, 30, 40}, USD: 0.002, LatencyMS: 1800, CacheMissAfterFirst: false, Outcome: "accepted",
		TS: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
	}
}

func sampleGame() GameRecord {
	return GameRecord{
		RunID: "r1", GameID: "g001", ServerGameID: "abc", Pairing: 0, Index: 0, White: "haiku", Black: "random",
		Seed: 42, Result: "1-0", Reason: "checkmate", Plies: 2, FinalFEN: startFEN,
		Actions: []ActionLogEntry{{Ply: 0, Seat: "w", Action: "e2e4", Talk: "gl"}}, Memos: []MemoEntry{{Ply: 0, Seat: "w", Memo: "x"}},
		USD: 0.1, Started: time.Unix(0, 0).UTC(), Ended: time.Unix(1, 0).UTC(),
	}
}

func TestDecisionRecordShapeGolden(t *testing.T) {
	golden(t, "records/decision_keys", keySet(t, sampleDecision()))
}

func TestGameRecordShapeGolden(t *testing.T) {
	golden(t, "records/game_keys", keySet(t, sampleGame()))
}

func TestDecisionRecordHasCacheReadTokens(t *testing.T) {
	ks := keySet(t, sampleDecision())
	for _, k := range []string{"tokens.cache_read_input_tokens", "tokens.cache_creation_input_tokens",
		"attempt_log[].tokens.cache_read_input_tokens", "cache_miss_after_first", "max_tokens_stops"} {
		if !strings.Contains(ks, k+"\n") {
			t.Fatalf("decision record lacks %s", k)
		}
	}
}

func TestDecisionRecordOmitsMemoText(t *testing.T) {
	raw, _ := json.Marshal(sampleDecision())
	if bytes.Contains(raw, []byte(`"memo"`)) {
		t.Fatal("memo text belongs in the game record, not the decision log")
	}
}

func TestRecorderWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Decision(sampleDecision())
	rec.Decision(sampleDecision())
	rec.Game(sampleGame())
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "decisions.jsonl"))
	if n := bytes.Count(b, []byte("\n")); n != 2 {
		t.Fatalf("decisions lines %d", n)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "games.jsonl"))
	var g GameRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &g); err != nil || g.Result != "1-0" {
		t.Fatalf("game line: %v %+v", err, g)
	}
	if len(rec.Decisions()) != 2 || len(rec.Games()) != 1 {
		t.Fatal("recorder must keep records for the summary")
	}
}

func TestWilson(t *testing.T) {
	lo, hi := Wilson(5, 10)
	if math.Abs(lo-0.2366) > 1e-3 || math.Abs(hi-0.7634) > 1e-3 {
		t.Fatalf("wilson(5,10) = %v %v", lo, hi)
	}
	lo, hi = Wilson(10, 10)
	if math.Abs(lo-0.7225) > 1e-3 || hi != 1 {
		t.Fatalf("wilson(10,10) = %v %v", lo, hi)
	}
	lo, hi = Wilson(0, 0)
	if lo != 0 || hi != 1 {
		t.Fatalf("wilson(0,0) = %v %v", lo, hi)
	}
}

func TestSummarize(t *testing.T) {
	d := func(player, game string, ply int, usd float64, lat int64, attempts, illegal, maxTok int, miss bool) DecisionRecord {
		return DecisionRecord{Player: player, Model: "m-" + player, GameID: game, Ply: ply, Seat: "w", USD: usd, LatencyMS: lat,
			Attempts: attempts, IllegalAttempts: illegal, MaxTokensStops: maxTok, CacheMissAfterFirst: miss, Outcome: "accepted"}
	}
	decs := []DecisionRecord{
		d("a", "g1", 0, 0.01, 1000, 1, 0, 0, false),
		d("a", "g1", 2, 0.03, 3000, 3, 2, 1, true),
		d("a", "g2", 1, 0.02, 2000, 1, 0, 0, false),
		d("b", "g1", 1, 0, 0, 0, 0, 0, false),
	}
	games := []GameRecord{
		{GameID: "g1", White: "a", Black: "b", Result: "1-0"},
		{GameID: "g2", White: "b", Black: "a", Result: "1/2-1/2"},
		{GameID: "g3", White: "a", Black: "b", Result: "aborted"},
	}
	ps := Summarize(decs, games)
	if len(ps) != 2 || ps[0].Player != "a" {
		t.Fatalf("%+v", ps)
	}
	a := ps[0]
	if a.W != 1 || a.D != 1 || a.L != 0 || a.Aborted != 1 || a.Games != 2 {
		t.Fatalf("wdl %+v", a)
	}
	if a.ScoreRate != 0.75 {
		t.Fatalf("score rate %v", a.ScoreRate)
	}
	if a.Decisions != 3 || a.Attempts != 5 || a.IllegalAttempts != 2 || a.MaxTokensStops != 1 {
		t.Fatalf("counts %+v", a)
	}
	if math.Abs(a.USDPerMove-0.02) > 1e-12 || math.Abs(a.IllegalRate-0.4) > 1e-12 {
		t.Fatalf("rates %+v", a)
	}
	if a.LatencyMeanMS != 2000 || a.LatencyP95MS != 3000 {
		t.Fatalf("latency %+v", a)
	}
	if len(a.CacheMisses) != 1 || a.CacheMisses[0] != "g1 ply 2 (w)" {
		t.Fatalf("cache misses %v", a.CacheMisses)
	}
	b := ps[1]
	if b.L != 1 || b.D != 1 || b.USD != 0 || b.IllegalRate != 0 {
		t.Fatalf("b %+v", b)
	}
}
