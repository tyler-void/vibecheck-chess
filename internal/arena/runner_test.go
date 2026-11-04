package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kisschess/internal/chess"
	kisshttp "kisschess/internal/http"
)

func TestScheduleSwapsColours(t *testing.T) {
	cfg := &MatchConfig{Pairings: []Pairing{{A: "x", B: "y", Games: 4}, {A: "z", B: "x", Games: 2}}}
	s := Schedule(cfg, 7)
	want := [][2]string{{"x", "y"}, {"y", "x"}, {"x", "y"}, {"y", "x"}, {"z", "x"}, {"x", "z"}}
	if len(s) != len(want) {
		t.Fatalf("%d games", len(s))
	}
	seen := map[uint64]bool{}
	for i, g := range s {
		if g.white != want[i][0] || g.black != want[i][1] {
			t.Fatalf("game %d: %s-%s want %v", i, g.white, g.black, want[i])
		}
		seen[g.seed] = true
	}
	if len(seen) != len(s) {
		t.Fatal("seeds must differ per game")
	}
	if Schedule(cfg, 7)[3].seed != s[3].seed || Schedule(cfg, 8)[3].seed == s[3].seed {
		t.Fatal("seeds must be a function of the run seed")
	}
}

func runnerCfg(t *testing.T, tweak func(*MatchConfig)) *MatchConfig {
	t.Helper()
	srv := httptest.NewServer(kisshttp.NewServer(kisshttp.Options{Manager: chess.NewGameManager(), Logger: quietLog()}))
	t.Cleanup(srv.Close)
	c := testConfig(srv.URL)
	c.OutDir = t.TempDir()
	c.RunSeed = 99
	c.Concurrency = 1
	if tweak != nil {
		tweak(c)
	}
	return c
}

func TestRunMatchBudgetStopsNewGames(t *testing.T) {
	// Each model call costs 1000*1/1e6 = 0.001; with ply cap 4 the model
	// seat decides twice per game: 0.002 per game. Prior 0.005 per game.
	cfg := runnerCfg(t, func(c *MatchConfig) {
		c.PlyCap = 4
		c.MaxUSD = 0.0105
		c.Prices["m"] = Price{Input: 1}
		c.Players["llm"] = PlayerConfig{Model: "m", EstUSDPerGame: 0.005, MaxTokens: 16000, PromptPack: "chess/v1"}
		c.Pairings = []Pairing{{A: "llm", B: "rand", Games: 10}}
	})
	m := newFake(nil, nil)
	m.usage = Usage{In: 1000}
	sum, err := RunMatch(context.Background(), cfg, m, RunOptions{RunID: "r", Log: quietLog(),
		BackoffBase: time.Millisecond, BackoffMax: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	// starts: 0+.005 ok; .002+.005 ok; .004+.005 ok; .006+.005 > .0105 stop.
	if !sum.StoppedByBudget || sum.GamesStarted != 3 || sum.GamesFinished != 3 || sum.GamesPlanned != 10 {
		t.Fatalf("%+v", sum)
	}
	if sum.SpentUSD < 0.0059 || sum.SpentUSD > 0.0061 {
		t.Fatalf("spent %v", sum.SpentUSD)
	}
}

func TestRunMatchWritesFilesWithoutKey(t *testing.T) {
	cfg := runnerCfg(t, func(c *MatchConfig) {
		c.PlyCap = 6
		c.Concurrency = 2
		c.Pairings = []Pairing{{A: "llm", B: "rand", Games: 2}, {A: "rand", B: "rand2", Games: 2}}
	})
	sum, err := RunMatch(context.Background(), cfg, newFake(nil, nil), RunOptions{RunID: "r1", Log: quietLog()})
	if err != nil {
		t.Fatal(err)
	}
	if sum.GamesFinished != 4 || sum.RunSeed != 99 {
		t.Fatalf("%+v", sum)
	}
	dir := filepath.Join(cfg.OutDir, "r1")
	for _, f := range []string{"decisions.jsonl", "games.jsonl", "summary.json", "summary.txt", "match.yaml"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v", f, err)
		}
		if bytes.Contains(b, []byte("sk-ant")) || bytes.Contains(bytes.ToLower(b), []byte("api_key")) {
			t.Fatalf("%s contains key-like text", f)
		}
	}
	var s Summary
	b, _ := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err := json.Unmarshal(b, &s); err != nil || len(s.Players) != 3 {
		t.Fatalf("summary %v %+v", err, s)
	}
	for _, p := range s.Players {
		if p.Player == "llm" && (p.Games != 2 || p.Decisions != 6 || p.USDPerMove <= 0) {
			t.Fatalf("llm summary %+v", p)
		}
	}
	txt, _ := os.ReadFile(filepath.Join(dir, "summary.txt"))
	if !strings.Contains(string(txt), "usd/move") || !strings.Contains(string(txt), "illegal%") {
		t.Fatalf("summary.txt:\n%s", txt)
	}
}

func TestRunMatchInterrupted(t *testing.T) {
	cfg := runnerCfg(t, func(c *MatchConfig) {
		c.Pairings = []Pairing{{A: "llm", B: "rand", Games: 2}}
	})
	m := newFake([]step{{slow: true}}, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel(errors.New("interrupt signal received")) }()
	sum, err := RunMatch(ctx, cfg, m, RunOptions{RunID: "r", Log: quietLog()})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Interrupted || sum.FatalError != "" || sum.GamesStarted != 1 || sum.GamesAborted != 1 {
		t.Fatalf("%+v", sum)
	}
	b, _ := os.ReadFile(filepath.Join(cfg.OutDir, "r", "games.jsonl"))
	var g GameRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &g); err != nil || g.Reason != ReasonAbortInterrupted {
		t.Fatalf("%v %+v", err, g)
	}
}

func TestRunMatchFatalStops(t *testing.T) {
	cfg := runnerCfg(t, func(c *MatchConfig) {
		c.Pairings = []Pairing{{A: "llm", B: "rand", Games: 4}}
	})
	m := newFake([]step{{err: &ModelError{Kind: ModelFatal, Status: 401, Code: "Unauthorized"}}}, nil)
	sum, err := RunMatch(context.Background(), cfg, m, RunOptions{RunID: "r", Log: quietLog()})
	if err != nil {
		t.Fatal(err)
	}
	if sum.FatalError == "" || sum.GamesStarted != 1 {
		t.Fatalf("%+v", sum)
	}
}
