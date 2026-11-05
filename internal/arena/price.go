// Package arena is the agent-arena harness: it plays chess matches between
// Claude models and baseline bots against an unmodified chess server, over
// HTTP only. The server stays the sole authority for legality, turn, clocks
// and outcome; the harness adds the arena rules (strikes, forfeits, talk,
// draw-offer limit, ply cap, budget) on top.
package arena

import (
	"fmt"
	"sync"
)

// LongPrompt is a price step: when a request's total prompt tokens exceed
// AboveInputTokens, every token of that request costs Multiplier times more
// (Haiku 5.5's 100K step, for example).
type LongPrompt struct {
	AboveInputTokens int64   `yaml:"above_input_tokens" json:"above_input_tokens"`
	Multiplier       float64 `yaml:"multiplier" json:"multiplier"`
}

// Price is USD per million tokens. Values come from the match file only.
type Price struct {
	Input      float64     `yaml:"input" json:"input"`
	Output     float64     `yaml:"output" json:"output"`
	CacheRead  float64     `yaml:"cache_read" json:"cache_read"`
	CacheWrite float64     `yaml:"cache_write" json:"cache_write"`
	LongPrompt *LongPrompt `yaml:"long_prompt" json:"long_prompt,omitempty"`
}

// Prices maps a model id to its price.
type Prices map[string]Price

// For returns the price of model or an error if the match file has none.
func (ps Prices) For(model string) (Price, error) {
	p, ok := ps[model]
	if !ok {
		return Price{}, fmt.Errorf("arena: no price for model %q", model)
	}
	return p, nil
}

// Usage is the token accounting of one or more model requests, keyed like
// the Messages API usage block.
type Usage struct {
	In         int64 `json:"input_tokens"`
	Out        int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// Add returns the field-wise sum.
func (u Usage) Add(o Usage) Usage {
	return Usage{u.In + o.In, u.Out + o.Out, u.CacheRead + o.CacheRead, u.CacheWrite + o.CacheWrite}
}

// Cost estimates the USD of one request. It is the only place USD is computed.
func (p Price) Cost(u Usage) float64 {
	usd := (float64(u.In)*p.Input + float64(u.Out)*p.Output +
		float64(u.CacheRead)*p.CacheRead + float64(u.CacheWrite)*p.CacheWrite) / 1e6
	if p.LongPrompt != nil && u.In+u.CacheRead+u.CacheWrite > p.LongPrompt.AboveInputTokens {
		usd *= p.LongPrompt.Multiplier
	}
	return usd
}

// Budget gates game starts against max_usd. A game may start only while
// spent + Σ in-flight estimates + its own prior stays within the cap; games
// already in flight always finish, even past the cap.
type Budget struct {
	mu       sync.Mutex
	cap      float64
	spent    float64
	inflight map[string]*flight
}

type flight struct{ spent, prior float64 }

// NewBudget returns a budget with the given cap in USD.
func NewBudget(capUSD float64) *Budget {
	return &Budget{cap: capUSD, inflight: map[string]*flight{}}
}

// TryStart registers game id with its prior estimate if it fits the cap.
func (b *Budget) TryStart(id string, prior float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	est := b.spent
	for _, f := range b.inflight {
		est += max(0, f.prior-f.spent) // spent is already in b.spent
	}
	if est+prior > b.cap {
		return false
	}
	b.inflight[id] = &flight{prior: prior}
	return true
}

// Charge adds usd spent by game id.
func (b *Budget) Charge(id string, usd float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent += usd
	if f, ok := b.inflight[id]; ok {
		f.spent += usd
	}
}

// Finish drops game id's in-flight estimate; its real spend stays counted.
func (b *Budget) Finish(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inflight, id)
}

// Spent returns the total estimated USD charged so far.
func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}
