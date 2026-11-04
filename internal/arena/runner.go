package arena

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Summary is summary.json.
type Summary struct {
	RunID           string          `json:"run_id"`
	RunSeed         uint64          `json:"run_seed"`
	MaxUSD          float64         `json:"max_usd"`
	SpentUSD        float64         `json:"spent_usd"`
	GamesPlanned    int             `json:"games_planned"`
	GamesStarted    int             `json:"games_started"`
	GamesFinished   int             `json:"games_finished"`
	GamesAborted    int             `json:"games_aborted"`
	StoppedByBudget bool            `json:"stopped_by_budget"`
	Interrupted     bool            `json:"interrupted"`
	FatalError      string          `json:"fatal_error"`
	Players         []PlayerSummary `json:"players"`
}

// RunOptions are the per-run knobs that are not part of the match file.
type RunOptions struct {
	RunID       string
	Log         *slog.Logger
	BackoffBase time.Duration // default 1s
	BackoffMax  time.Duration // default 30s
}

type scheduled struct {
	id             string
	pairing, index int
	white, black   string
	seed           uint64
}

// Schedule lists every game in order: pairing by pairing, A taking White in
// even-numbered games, with a deterministic seed per game.
func Schedule(cfg *MatchConfig, runSeed uint64) []scheduled {
	r := mrand.New(mrand.NewPCG(runSeed, 0x6172656e61)) // "arena"
	var out []scheduled
	for pi, p := range cfg.Pairings {
		for i := range p.Games {
			w, b := p.A, p.B
			if i%2 == 1 {
				w, b = b, w
			}
			out = append(out, scheduled{id: fmt.Sprintf("g%03d", len(out)+1), pairing: pi, index: i, white: w, black: b, seed: r.Uint64()})
		}
	}
	return out
}

func resolvePlayers(cfg *MatchConfig) (map[string]*player, error) {
	sp := SystemParams{PlyCap: cfg.PlyCap, DrawOfferEvery: cfg.DrawOfferEvery, TimeoutSeconds: int(cfg.DecisionTimeout.Seconds())}
	out := map[string]*player{}
	for name, pc := range cfg.Players {
		p := &player{name: name, cfg: pc}
		if !pc.IsBot() {
			var err error
			if p.price, err = cfg.Prices.For(pc.Model); err != nil {
				return nil, err
			}
			if p.pack, err = LoadPack(pc.PromptPack); err != nil {
				return nil, err
			}
			if p.system, err = p.pack.System(sp); err != nil {
				return nil, err
			}
		}
		out[name] = p
	}
	return out, nil
}

// RunMatch plays the whole match and writes arena-data/<run-id>/.
func RunMatch(ctx context.Context, cfg *MatchConfig, model ModelClient, opt RunOptions) (Summary, error) {
	parent := ctx
	if opt.BackoffBase == 0 {
		opt.BackoffBase = time.Second
	}
	if opt.BackoffMax == 0 {
		opt.BackoffMax = 30 * time.Second
	}
	if opt.RunID == "" {
		opt.RunID = time.Now().UTC().Format("20060102-150405")
	}
	runSeed := cfg.RunSeed
	if runSeed == 0 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		runSeed = binary.LittleEndian.Uint64(b[:]) | 1
	}
	players, err := resolvePlayers(cfg)
	if err != nil {
		return Summary{}, err
	}
	rec, err := NewRecorder(filepath.Join(cfg.OutDir, opt.RunID))
	if err != nil {
		return Summary{}, err
	}
	snapshotCfg := *cfg
	snapshotCfg.RunSeed = runSeed
	if y, err := yaml.Marshal(snapshotCfg); err == nil {
		_ = rec.WriteFile("match.yaml", y)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	env := &Env{Cfg: cfg, Server: NewServerClient(cfg.Server), Model: model, Budget: NewBudget(cfg.MaxUSD), Rec: rec,
		Log: opt.Log, RunID: opt.RunID, BackoffBase: opt.BackoffBase, BackoffMax: opt.BackoffMax,
		OnFatal: func(err error) { cancel(err) }}

	games := Schedule(cfg, runSeed)
	sum := Summary{RunID: opt.RunID, RunSeed: runSeed, MaxUSD: cfg.MaxUSD, GamesPlanned: len(games)}
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for _, sg := range games {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		prior := players[sg.white].cfg.EstUSDPerGame + players[sg.black].cfg.EstUSDPerGame
		if !env.Budget.TryStart(sg.id, prior) {
			<-sem
			sum.StoppedByBudget = true
			opt.Log.Warn("budget cap reached; no new games", "spent_usd", fmt.Sprintf("%.4f", env.Budget.Spent()), "max_usd", cfg.MaxUSD)
			break
		}
		sum.GamesStarted++
		wg.Add(1)
		go func(sg scheduled) {
			defer wg.Done()
			defer func() { <-sem }()
			g := NewGame(env, sg.id, sg.pairing, sg.index, sg.seed, players[sg.white], players[sg.black])
			rec.Game(g.Play(ctx))
			env.Budget.Finish(sg.id)
		}(sg)
	}
	wg.Wait()

	for _, g := range rec.Games() {
		if g.Result == "aborted" {
			sum.GamesAborted++
		} else {
			sum.GamesFinished++
		}
	}
	switch {
	case parent.Err() != nil: // Ctrl-C / SIGTERM, whatever cause the signal context carries
		sum.Interrupted = true
	case ctx.Err() != nil:
		sum.FatalError = context.Cause(ctx).Error()
	}
	sum.SpentUSD = env.Budget.Spent()
	sum.Players = Summarize(rec.Decisions(), rec.Games())
	if b, err := json.MarshalIndent(sum, "", "  "); err == nil {
		_ = rec.WriteFile("summary.json", append(b, '\n'))
	}
	_ = rec.WriteFile("summary.txt", []byte(FormatRunSummary(sum)))
	return sum, rec.Close()
}

// FormatRunSummary renders summary.txt.
func FormatRunSummary(s Summary) string {
	head := fmt.Sprintf("run %s  seed %d\ngames planned %d, started %d, finished %d, aborted %d\nspent $%.4f of max $%.2f  stopped_by_budget=%v interrupted=%v",
		s.RunID, s.RunSeed, s.GamesPlanned, s.GamesStarted, s.GamesFinished, s.GamesAborted, s.SpentUSD, s.MaxUSD, s.StoppedByBudget, s.Interrupted)
	if s.FatalError != "" {
		head += "\nfatal: " + s.FatalError
	}
	out := head + "\n\n" + FormatSummary(s.Players)
	for _, p := range s.Players {
		if len(p.CacheMisses) > 0 {
			out += fmt.Sprintf("\n%s: cache_read_input_tokens = 0 after the game's first call at: %v\n", p.Player, p.CacheMisses)
		}
	}
	return out
}
