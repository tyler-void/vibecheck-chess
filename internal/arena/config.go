package arena

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// KeyEnv is the only environment variable the arena reads a provider key from.
const KeyEnv = "ARENA_ANTHROPIC_API_KEY"

// TimeControl is the server-side chess clock for every game.
type TimeControl struct {
	Initial   time.Duration `yaml:"initial"`
	Increment time.Duration `yaml:"increment"`
	Delay     time.Duration `yaml:"delay"`
}

// PlayerConfig is one player: a model with its settings, or a bot.
type PlayerConfig struct {
	Model         string  `yaml:"model"`
	Bot           string  `yaml:"bot"`
	Effort        string  `yaml:"effort"`   // "" = model default
	Thinking      string  `yaml:"thinking"` // "" = omitted, or "adaptive"
	MaxTokens     int64   `yaml:"max_tokens"`
	PromptPack    string  `yaml:"prompt_pack"`
	EstUSDPerGame float64 `yaml:"est_usd_per_game"`
}

// IsBot reports whether the player is a baseline bot.
func (p PlayerConfig) IsBot() bool { return p.Bot != "" }

// Pairing plays Games games between players A and B, A taking White in the
// even-numbered games (0, 2, ...) and B in the odd ones.
type Pairing struct {
	A     string `yaml:"a"`
	B     string `yaml:"b"`
	Games int    `yaml:"games"`
}

// MatchConfig is the whole match file.
type MatchConfig struct {
	Server          string        `yaml:"server"`
	OutDir          string        `yaml:"out_dir"`
	MaxUSD          float64       `yaml:"max_usd"`
	Concurrency     int           `yaml:"concurrency"`
	DecisionTimeout time.Duration `yaml:"decision_timeout"`
	PlyCap          int           `yaml:"ply_cap"`
	TimeControl     TimeControl   `yaml:"time_control"`
	DrawOfferEvery  int           `yaml:"draw_offer_every"`
	PollInterval    time.Duration `yaml:"poll_interval"`
	// A game whose spend exceeds MaxGameUSDFactor × the sum of its players'
	// est_usd_per_game is aborted with no result.
	MaxGameUSDFactor float64                 `yaml:"max_game_usd_factor"`
	RunSeed          uint64                  `yaml:"run_seed"` // 0 = random, recorded
	Prices           Prices                  `yaml:"prices"`
	Players          map[string]PlayerConfig `yaml:"players"`
	Pairings         []Pairing               `yaml:"pairings"`
}

// LoadConfig reads and decodes a match file, rejecting unknown fields, and
// fills defaults. Call Validate before use.
func LoadConfig(path string) (*MatchConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c MatchConfig
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("arena: %s: %w", path, err)
	}
	c.applyDefaults()
	return &c, nil
}

func (c *MatchConfig) applyDefaults() {
	if c.OutDir == "" {
		c.OutDir = "arena-data"
	}
	if c.Concurrency == 0 {
		c.Concurrency = 2
	}
	if c.DecisionTimeout == 0 {
		c.DecisionTimeout = 180 * time.Second
	}
	if c.PlyCap == 0 {
		c.PlyCap = 400
	}
	if c.TimeControl.Initial == 0 {
		c.TimeControl.Initial = 2 * time.Hour
	}
	if c.DrawOfferEvery == 0 {
		c.DrawOfferEvery = 10
	}
	if c.MaxGameUSDFactor == 0 {
		c.MaxGameUSDFactor = 3
	}
	if c.PollInterval == 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	for name, p := range c.Players {
		if !p.IsBot() {
			if p.MaxTokens == 0 {
				p.MaxTokens = 16000
			}
			if p.PromptPack == "" {
				p.PromptPack = "chess/v1"
			}
		}
		c.Players[name] = p
	}
}

var validEffort = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// Validate checks the match file. hasKey tells whether ARENA_ANTHROPIC_API_KEY
// is set; it is required only when a model player is present.
func (c *MatchConfig) Validate(hasKey bool) error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if c.Server == "" {
		bad("server is required")
	}
	if c.MaxUSD <= 0 {
		bad("max_usd must be > 0")
	}
	if c.Concurrency < 1 || c.PlyCap < 1 || c.DrawOfferEvery < 1 || c.DecisionTimeout <= 0 {
		bad("concurrency, ply_cap, draw_offer_every and decision_timeout must be positive")
	}
	if c.MaxGameUSDFactor < 1 {
		bad("max_game_usd_factor must be >= 1")
	}
	if c.TimeControl.Increment < 0 || c.TimeControl.Delay < 0 {
		bad("time_control increment and delay must be >= 0")
	}
	for model, p := range c.Prices {
		if p.Input < 0 || p.Output < 0 || p.CacheRead < 0 || p.CacheWrite < 0 {
			bad("price for %s: negative value", model)
		}
		if p.LongPrompt != nil && (p.LongPrompt.AboveInputTokens <= 0 || p.LongPrompt.Multiplier < 1) {
			bad("price for %s: long_prompt needs above_input_tokens > 0 and multiplier >= 1", model)
		}
	}
	needKey := false
	names := make([]string, 0, len(c.Players))
	for n := range c.Players {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := c.Players[n]
		switch {
		case (p.Model == "") == (p.Bot == ""):
			bad("player %s: set exactly one of model or bot", n)
		case p.IsBot():
			if p.Bot != "random" {
				bad("player %s: unknown bot %q (only \"random\")", n, p.Bot)
			}
		default:
			needKey = true
			if _, err := c.Prices.For(p.Model); err != nil {
				bad("player %s: no price for model %s", n, p.Model)
			}
			if !validEffort[p.Effort] {
				bad("player %s: effort %q is not one of low|medium|high|xhigh|max", n, p.Effort)
			}
			if p.Thinking != "" && p.Thinking != "adaptive" {
				bad("player %s: thinking %q not allowed (omit it or use adaptive)", n, p.Thinking)
			}
			if p.EstUSDPerGame <= 0 {
				bad("player %s: est_usd_per_game must be > 0", n)
			}
			if p.MaxTokens < 1024 {
				bad("player %s: max_tokens must be >= 1024", n)
			}
		}
	}
	if len(c.Pairings) == 0 {
		bad("pairings: at least one is required")
	}
	for i, pr := range c.Pairings {
		_, okA := c.Players[pr.A]
		_, okB := c.Players[pr.B]
		if !okA || !okB {
			bad("pairing %d: unknown player %q or %q", i, pr.A, pr.B)
		}
		if pr.A == pr.B {
			bad("pairing %d: a player cannot play itself", i)
		}
		if pr.Games <= 0 || pr.Games%2 != 0 {
			bad("pairing %d: games must be even and > 0 (colours swap every game)", i)
		}
	}
	if needKey && !hasKey {
		bad("%s is not set and the match has model players", KeyEnv)
	}
	return errors.Join(errs...)
}
