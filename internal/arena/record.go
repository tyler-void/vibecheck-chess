package arena

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AttemptLog is one model request (or bot pick) inside a decision.
type AttemptLog struct {
	Kind          string  `json:"kind"` // accepted|illegal|malformed|max_tokens|timeout|provider_error|server_error|stale
	Code          string  `json:"code,omitempty"`
	Action        string  `json:"action,omitempty"`
	StopReason    string  `json:"stop_reason,omitempty"`
	ResponseModel string  `json:"response_model,omitempty"`
	LatencyMS     int64   `json:"latency_ms"`
	Tokens        Usage   `json:"tokens"`
	USD           float64 `json:"usd"`
}

// DecisionRecord is one line of decisions.jsonl.
type DecisionRecord struct {
	RunID               string       `json:"run_id"`
	GameID              string       `json:"game_id"`
	ServerGameID        string       `json:"server_game_id"`
	Seat                string       `json:"seat"`
	Player              string       `json:"player"`
	Model               string       `json:"model"`
	Effort              string       `json:"effort"`
	PromptPack          string       `json:"prompt_pack"`
	ViewSeq             uint64       `json:"view_seq"`
	Ply                 int          `json:"ply"`
	Mode                string       `json:"mode"`
	Action              string       `json:"action"`
	Talk                string       `json:"talk"`
	MemoLen             int          `json:"memo_len"`
	Attempts            int          `json:"attempts"`
	IllegalAttempts     int          `json:"illegal_attempts"`
	MaxTokensStops      int          `json:"max_tokens_stops"`
	Timeouts            int          `json:"timeouts"`
	ProviderRetries     int          `json:"provider_retries"`
	ServerRetries       int          `json:"server_retries"`
	StaleResends        int          `json:"stale_resends"`
	AttemptLog          []AttemptLog `json:"attempt_log"`
	Tokens              Usage        `json:"tokens"`
	USD                 float64      `json:"usd"`
	LatencyMS           int64        `json:"latency_ms"`
	CacheMissAfterFirst bool         `json:"cache_miss_after_first"`
	Outcome             string       `json:"outcome"` // accepted|forfeit|aborted|game_over|cancelled
	Note                string       `json:"note"`
	TS                  time.Time    `json:"ts"`
}

// ActionLogEntry is one accepted action, in order. Replaying the move
// entries from the start position reproduces the game.
type ActionLogEntry struct {
	Ply    int    `json:"ply"`
	Seat   string `json:"seat"`
	Action string `json:"action"`
	Talk   string `json:"talk,omitempty"`
}

// MemoEntry is a private memo; kept in the game record only.
type MemoEntry struct {
	Ply  int    `json:"ply"`
	Seat string `json:"seat"`
	Memo string `json:"memo"`
}

// GameRecord is one line of games.jsonl.
type GameRecord struct {
	RunID        string           `json:"run_id"`
	GameID       string           `json:"game_id"`
	ServerGameID string           `json:"server_game_id"`
	Pairing      int              `json:"pairing"`
	Index        int              `json:"index"`
	White        string           `json:"white"`
	Black        string           `json:"black"`
	Seed         uint64           `json:"seed"`
	Result       string           `json:"result"` // 1-0 | 0-1 | 1/2-1/2 | aborted
	Reason       string           `json:"reason"`
	Plies        int              `json:"plies"`
	FinalFEN     string           `json:"final_fen"`
	Actions      []ActionLogEntry `json:"actions"`
	Memos        []MemoEntry      `json:"memos"`
	USD          float64          `json:"usd"`
	Started      time.Time        `json:"started"`
	Ended        time.Time        `json:"ended"`
}

// Recorder appends records as JSONL and keeps them for the summary.
type Recorder struct {
	mu        sync.Mutex
	dir       string
	dec, game *os.File
	decW      *bufio.Writer
	gameW     *bufio.Writer
	decisions []DecisionRecord
	games     []GameRecord
	err       error
}

// NewRecorder creates dir and the JSONL files in it.
func NewRecorder(dir string) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dec, err := os.Create(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		return nil, err
	}
	game, err := os.Create(filepath.Join(dir, "games.jsonl"))
	if err != nil {
		dec.Close()
		return nil, err
	}
	return &Recorder{dir: dir, dec: dec, game: game, decW: bufio.NewWriter(dec), gameW: bufio.NewWriter(game)}, nil
}

// Dir returns the run directory.
func (r *Recorder) Dir() string { return r.dir }

func (r *Recorder) write(w *bufio.Writer, v any) {
	b, err := json.Marshal(v)
	if err == nil {
		_, err = w.Write(append(b, '\n'))
	}
	if err == nil {
		err = w.Flush() // keep files valid if the run dies
	}
	if err != nil && r.err == nil {
		r.err = err
	}
}

// Decision appends a decision record.
func (r *Recorder) Decision(d DecisionRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decisions = append(r.decisions, d)
	r.write(r.decW, d)
}

// Game appends a game record.
func (r *Recorder) Game(g GameRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.games = append(r.games, g)
	r.write(r.gameW, g)
}

// Decisions returns a copy of all decision records.
func (r *Recorder) Decisions() []DecisionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]DecisionRecord(nil), r.decisions...)
}

// Games returns a copy of all game records.
func (r *Recorder) Games() []GameRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]GameRecord(nil), r.games...)
}

// WriteFile writes an extra file into the run directory.
func (r *Recorder) WriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Join(r.dir, name), data, 0o644)
}

// Close flushes and closes the files.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.err, r.decW.Flush(), r.gameW.Flush(), r.dec.Close(), r.game.Close())
}

// PlayerSummary aggregates one player's decisions and games.
type PlayerSummary struct {
	Player          string   `json:"player"`
	Model           string   `json:"model"`
	Games           int      `json:"games"` // finished, with a result
	W               int      `json:"w"`
	D               int      `json:"d"`
	L               int      `json:"l"`
	Aborted         int      `json:"aborted"`
	ScoreRate       float64  `json:"score_rate"`
	WilsonLo        float64  `json:"wilson95_lo"`
	WilsonHi        float64  `json:"wilson95_hi"`
	Decisions       int      `json:"decisions"`
	Attempts        int      `json:"attempts"`
	IllegalAttempts int      `json:"illegal_attempts"`
	IllegalRate     float64  `json:"illegal_attempt_rate"` // illegal_attempts / attempts
	MaxTokensStops  int      `json:"max_tokens_stops"`
	Timeouts        int      `json:"timeouts"`
	ProviderRetries int      `json:"provider_retries"`
	USD             float64  `json:"usd"`
	USDPerMove      float64  `json:"usd_per_move"` // per decision
	LatencyMeanMS   int64    `json:"latency_mean_ms_per_move"`
	LatencyP95MS    int64    `json:"latency_p95_ms_per_move"`
	CacheReadTokens int64    `json:"cache_read_input_tokens"`
	CacheMisses     []string `json:"cache_misses_after_first"`
}

// Wilson returns the 95% Wilson score interval for successes out of n.
func Wilson(successes float64, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	const z = 1.959963984540054
	nf := float64(n)
	p := successes / nf
	den := 1 + z*z/nf
	c := (p + z*z/(2*nf)) / den
	h := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / den
	return max(0, c-h), min(1, c+h)
}

// Summarize aggregates per player, sorted by player name.
func Summarize(decs []DecisionRecord, games []GameRecord) []PlayerSummary {
	by := map[string]*PlayerSummary{}
	lat := map[string][]int64{}
	get := func(p string) *PlayerSummary {
		if s, ok := by[p]; ok {
			return s
		}
		s := &PlayerSummary{Player: p, CacheMisses: []string{}}
		by[p] = s
		return s
	}
	for _, d := range decs {
		s := get(d.Player)
		if s.Model == "" {
			s.Model = d.Model
		}
		s.Decisions++
		s.Attempts += d.Attempts
		s.IllegalAttempts += d.IllegalAttempts
		s.MaxTokensStops += d.MaxTokensStops
		s.Timeouts += d.Timeouts
		s.ProviderRetries += d.ProviderRetries
		s.USD += d.USD
		s.CacheReadTokens += d.Tokens.CacheRead
		lat[d.Player] = append(lat[d.Player], d.LatencyMS)
		if d.CacheMissAfterFirst {
			s.CacheMisses = append(s.CacheMisses, fmt.Sprintf("%s ply %d (%s)", d.GameID, d.Ply, d.Seat))
		}
	}
	score := map[string]float64{}
	for _, g := range games {
		w, b := get(g.White), get(g.Black)
		switch g.Result {
		case "1-0":
			w.W++
			b.L++
			score[g.White]++
		case "0-1":
			b.W++
			w.L++
			score[g.Black]++
		case "1/2-1/2":
			w.D++
			b.D++
			score[g.White] += 0.5
			score[g.Black] += 0.5
		default:
			w.Aborted++
			b.Aborted++
			continue
		}
		w.Games++
		b.Games++
	}
	out := make([]PlayerSummary, 0, len(by))
	for p, s := range by {
		if s.Games > 0 {
			s.ScoreRate = score[p] / float64(s.Games)
		}
		s.WilsonLo, s.WilsonHi = Wilson(score[p], s.Games)
		if s.Attempts > 0 {
			s.IllegalRate = float64(s.IllegalAttempts) / float64(s.Attempts)
		}
		if s.Decisions > 0 {
			s.USDPerMove = s.USD / float64(s.Decisions)
		}
		if l := lat[p]; len(l) > 0 {
			sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
			var sum int64
			for _, v := range l {
				sum += v
			}
			s.LatencyMeanMS = sum / int64(len(l))
			s.LatencyP95MS = l[int(math.Ceil(0.95*float64(len(l))))-1]
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Player < out[j].Player })
	return out
}

// FormatSummary renders a plain-text table for summary.txt.
func FormatSummary(ps []PlayerSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-10s %-18s %5s %9s %6s %13s %9s %10s %9s %8s %8s %6s %s\n",
		"player", "model", "games", "W-D-L", "score", "wilson95", "decisions", "usd/move", "lat_mean", "lat_p95", "illegal%", "maxtok", "cache_misses")
	for _, p := range ps {
		model := p.Model
		if model == "" {
			model = "(bot)"
		}
		fmt.Fprintf(&b, "%-10s %-18s %5d %9s %6.3f %13s %9d %10.5f %8.1fs %7.1fs %8.2f %6d %d\n",
			p.Player, model, p.Games, fmt.Sprintf("%d-%d-%d", p.W, p.D, p.L), p.ScoreRate,
			fmt.Sprintf("[%.2f,%.2f]", p.WilsonLo, p.WilsonHi), p.Decisions, p.USDPerMove,
			float64(p.LatencyMeanMS)/1000, float64(p.LatencyP95MS)/1000, 100*p.IllegalRate, p.MaxTokensStops, len(p.CacheMisses))
	}
	return b.String()
}
