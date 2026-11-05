package arena

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
server: http://localhost:8080
max_usd: 15
prices:
  claude-haiku-5-5: {input: 0.10, output: 0.50, cache_read: 0.01, cache_write: 0.125,
                     long_prompt: {above_input_tokens: 100000, multiplier: 5}}
  claude-opus-5-5:  {input: 4.00, output: 20.00, cache_read: 0.20, cache_write: 5.00}
players:
  haiku:  {model: claude-haiku-5-5, est_usd_per_game: 0.10}
  opus:   {model: claude-opus-5-5, effort: medium, est_usd_per_game: 2.00}
  random: {bot: random}
pairings:
  - {a: haiku, b: random, games: 10}
  - {a: opus,  b: haiku,  games: 4}
`

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(writeCfg(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(true); err != nil {
		t.Fatal(err)
	}
	if c.DecisionTimeout != 180*time.Second || c.PlyCap != 400 || c.DrawOfferEvery != 10 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.TimeControl.Initial != 2*time.Hour || c.TimeControl.Increment != 0 || c.TimeControl.Delay != 0 {
		t.Fatalf("time control: %+v", c.TimeControl)
	}
	if c.MaxGameUSDFactor != 3 {
		t.Fatalf("max_game_usd_factor default %v", c.MaxGameUSDFactor)
	}
	if c.OutDir != "arena-data" || c.Concurrency != 2 || c.PollInterval != 250*time.Millisecond {
		t.Fatalf("defaults: %+v", c)
	}
	h := c.Players["haiku"]
	if h.MaxTokens != 16000 || h.PromptPack != "chess/v1" || h.Effort != "" {
		t.Fatalf("player defaults: %+v", h)
	}
	if c.Prices["claude-haiku-5-5"].LongPrompt.Multiplier != 5 {
		t.Fatal("long prompt not parsed")
	}
}

func TestExampleConfigsAreValid(t *testing.T) {
	for _, name := range []string{"arena.example.yaml", "arena.gate.yaml"} {
		c, err := LoadConfig(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := c.Validate(true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	gate, _ := LoadConfig(filepath.Join("..", "..", "arena.gate.yaml"))
	if gate.MaxUSD != 15 {
		t.Fatalf("gate max_usd %v", gate.MaxUSD)
	}
	for name, p := range gate.Players {
		if p.Model == "claude-opus-5-5" && p.Effort == "" {
			t.Fatalf("gate player %s: Opus effort must be pinned", name)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
		hasKey           bool
	}{
		{"unknown field", validYAML + "\nbogus: 1\n", "bogus", true},
		{"odd games", strings.Replace(validYAML, "games: 4", "games: 3", 1), "even", true},
		{"zero games", strings.Replace(validYAML, "games: 4", "games: 0", 1), "even", true},
		{"missing price", strings.Replace(validYAML, "claude-opus-5-5:  {input", "claude-x:  {input", 1), "no price", true},
		{"unknown player", strings.Replace(validYAML, "b: haiku", "b: sonnet", 1), "unknown player", true},
		{"same player both sides", strings.Replace(validYAML, "b: haiku", "b: opus", 1), "itself", true},
		{"no cap", strings.Replace(validYAML, "max_usd: 15", "max_usd: 0", 1), "max_usd", true},
		{"no estimate", strings.Replace(validYAML, ", est_usd_per_game: 2.00", "", 1), "est_usd_per_game", true},
		{"bad effort", strings.Replace(validYAML, "effort: medium", "effort: huge", 1), "effort", true},
		{"thinking disabled rejected", strings.Replace(validYAML, "effort: medium", "thinking: disabled", 1), "thinking", true},
		{"model and bot", strings.Replace(validYAML, "{bot: random}", "{bot: random, model: claude-opus-5-5}", 1), "exactly one", true},
		{"unknown bot", strings.Replace(validYAML, "{bot: random}", "{bot: stockfish}", 1), "bot", true},
		{"missing key", validYAML, "ARENA_ANTHROPIC_API_KEY", false},
		{"no pairings", strings.Split(validYAML, "pairings:")[0], "pairings", true},
		{"game factor below 1", validYAML + "\nmax_game_usd_factor: 0.5\n", "max_game_usd_factor", true},
		{"negative price", strings.Replace(validYAML, "input: 4.00", "input: -1", 1), "negative", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeCfg(t, c.body))
			if err == nil {
				err = cfg.Validate(c.hasKey)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestConfigBotsOnlyNeedsNoKey(t *testing.T) {
	body := `
server: http://x
max_usd: 1
players:
  r1: {bot: random}
  r2: {bot: random}
pairings: [{a: r1, b: r2, games: 2}]
`
	c, err := LoadConfig(writeCfg(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(false); err != nil {
		t.Fatal(err)
	}
}
