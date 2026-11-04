package arena

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

// Env is what every game shares within one run.
type Env struct {
	Cfg         *MatchConfig
	Server      *ServerClient
	Model       ModelClient // nil when the match has only bots
	Budget      *Budget
	Rec         *Recorder
	Log         *slog.Logger
	RunID       string
	BackoffBase time.Duration
	BackoffMax  time.Duration
	OnFatal     func(error) // a configuration-level API error: stop the run
}

func (e *Env) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// player is a resolved PlayerConfig.
type player struct {
	name   string
	cfg    PlayerConfig
	price  Price
	pack   *Pack
	system string
}

func (p *player) effortLabel() string {
	if p.cfg.IsBot() {
		return ""
	}
	if p.cfg.Effort == "" {
		return "default"
	}
	return p.cfg.Effort
}

const (
	white = 0
	black = 1
	none  = -1
)

var colorLetter = [2]string{"w", "b"}
var colorName = [2]string{"White", "Black"}

type talkMsg struct {
	text string
	n    int
}

// Game is one chess game between two seats.
type Game struct {
	env      *Env
	id       string
	serverID string
	pairing  int
	index    int
	seed     uint64
	players  [2]*player

	runCtx context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	replica    *Replica
	acting     int
	talk       [2]talkMsg
	liveOffer  int // colour of an offer the opponent has not answered yet
	actions    []ActionLogEntry
	memos      []MemoEntry
	reason     string // arena-level end reason, first one wins
	loser      int    // for forfeits
	modelCalls map[string]int
	usd        float64
}

// NewGame prepares a game; Play runs it.
func NewGame(env *Env, id string, pairing, index int, seed uint64, w, b *player) *Game {
	return &Game{env: env, id: id, pairing: pairing, index: index, seed: seed, players: [2]*player{w, b},
		replica: NewReplica(), acting: none, liveOffer: none, loser: none, modelCalls: map[string]int{}}
}

// ceiling is the per-game hard spend limit: max_game_usd_factor × the sum of
// both players' est_usd_per_game. 0 means no ceiling (bots only).
func (g *Game) ceiling() float64 {
	return g.env.Cfg.MaxGameUSDFactor * (g.players[white].cfg.EstUSDPerGame + g.players[black].cfg.EstUSDPerGame)
}

func (g *Game) seatID(c int) string {
	return fmt.Sprintf("%s-%s-%s", g.id, colorLetter[c], g.players[c].name)
}

// end records an arena-level end reason (first caller wins) and stops both seats.
func (g *Game) end(reason string, loser int) {
	g.mu.Lock()
	if g.reason == "" {
		g.reason, g.loser = reason, loser
	}
	g.mu.Unlock()
	g.cancel()
}

// Play runs the game to its end and returns its record.
func (g *Game) Play(ctx context.Context) GameRecord {
	env := g.env
	started := time.Now().UTC()
	rec := GameRecord{RunID: env.RunID, GameID: g.id, Pairing: g.pairing, Index: g.index,
		White: g.players[white].name, Black: g.players[black].name, Seed: g.seed, Started: started}

	g.runCtx = ctx
	var gctx context.Context
	gctx, g.cancel = context.WithCancel(ctx)
	defer g.cancel()

	st := NewStrikes(env.BackoffBase, env.BackoffMax)
	for {
		id, err := env.Server.CreateGame(ctx, g.seatID(white), g.seatID(black), env.Cfg.TimeControl)
		if err == nil {
			g.serverID = id
			break
		}
		var se *ServerError
		if !errors.As(err, &se) || !se.Transient() {
			rec.Result, rec.Reason, rec.Ended = "aborted", "aborted_create_failed: "+err.Error(), time.Now().UTC()
			return rec
		}
		if v := st.Next(ServerErr(se.Code)); v.Kind == VAbort || env.sleep(ctx, v.Wait) != nil {
			rec.Result, rec.Reason, rec.Ended = "aborted", ReasonAbortServer, time.Now().UTC()
			return rec
		}
	}
	rec.ServerGameID = g.serverID
	env.Log.Info("game start", "game", g.id, "server_game", g.serverID, "white", rec.White, "black", rec.Black,
		"watch", env.Server.Base+"/game/"+g.serverID)

	var wg sync.WaitGroup
	for c := range 2 {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			s := &seat{g: g, color: c, p: g.players[c], lastOfferAt: -1}
			if g.players[c].cfg.IsBot() {
				var key [32]byte
				binary.LittleEndian.PutUint64(key[:], g.seed)
				key[8] = byte(c)
				s.rng = rand.New(rand.NewChaCha8(key))
			}
			s.run(gctx)
		}(c)
	}
	wg.Wait()

	final, ferr := env.Server.Snapshot(context.WithoutCancel(ctx), g.serverID)
	g.mu.Lock()
	defer g.mu.Unlock()
	rec.Plies = g.replica.Plies()
	rec.FinalFEN = g.replica.FEN()
	rec.Actions, rec.Memos = g.actions, g.memos
	if rec.Memos == nil {
		rec.Memos = []MemoEntry{}
	}
	if rec.Actions == nil {
		rec.Actions = []ActionLogEntry{}
	}
	rec.USD = g.usd
	switch {
	case g.reason == ReasonForfeitIllegal || g.reason == ReasonForfeitTimeout:
		rec.Result, rec.Reason = map[int]string{white: "0-1", black: "1-0"}[g.loser], g.reason
	case g.reason == ReasonDrawPlyCap:
		rec.Result, rec.Reason = "1/2-1/2", g.reason
	case g.reason != "":
		rec.Result, rec.Reason = "aborted", g.reason
	case ctx.Err() != nil:
		rec.Result, rec.Reason = "aborted", ReasonAbortInterrupted
	case ferr != nil:
		rec.Result, rec.Reason = "aborted", "aborted_final_snapshot: "+ferr.Error()
	default:
		rec.Result = map[uint8]string{outcomeWhiteWin: "1-0", outcomeBlackWin: "0-1", outcomeDraw: "1/2-1/2"}[final.Outcome]
		if int(final.Termination) < len(terminationNames) {
			rec.Reason = terminationNames[final.Termination]
		}
		if rec.Result == "" {
			rec.Result, rec.Reason = "aborted", "aborted_unknown"
		}
	}
	if ferr == nil && final.FEN != rec.FinalFEN && rec.Result != "aborted" {
		env.Log.Error("final FEN differs from replica", "game", g.id, "server", final.FEN, "replica", rec.FinalFEN)
	}
	rec.Ended = time.Now().UTC()
	env.Log.Info("game end", "game", g.id, "result", rec.Result, "reason", rec.Reason, "plies", rec.Plies, "usd", fmt.Sprintf("%.4f", rec.USD))
	return rec
}

// More arena-level end reasons.
const (
	ReasonDrawPlyCap         = "draw_ply_cap"
	ReasonAbortInterrupted   = "aborted_interrupted"
	ReasonAbortFatal         = "aborted_fatal_api_error"
	ReasonAbortReplica       = "aborted_replica_mismatch"
	ReasonAbortModelMismatch = "aborted_model_mismatch"
	ReasonAbortGameBudget    = "aborted_game_budget"
)

// seat is one side's decision loop. Its fields are touched only by its own
// goroutine; shared state lives on Game under g.mu.
type seat struct {
	g           *Game
	color       int
	p           *player
	rng         *rand.Rand
	memo        string
	ownMoves    int
	lastOfferAt int // ownMoves at the last offer, -1 = never
	talkSeen    int
}

func (s *seat) opp() int { return 1 - s.color }

func (s *seat) run(ctx context.Context) {
	for {
		snap, ok := s.waitTurn(ctx)
		if !ok || snap.Outcome != outcomeOngoing {
			return
		}
		if !s.g.syncReplica(snap) {
			s.g.env.Log.Error("replica out of sync", "game", s.g.id, "server_fen", snap.FEN)
			s.g.end(ReasonAbortReplica, none)
			return
		}
		if s.g.plies() >= s.g.env.Cfg.PlyCap {
			s.g.endByCap(s, snap)
			return
		}
		if !s.turn(ctx, snap) {
			return
		}
	}
}

// waitTurn polls until it is this seat's turn (and the opponent has finished
// every post of its own turn) or the game is over.
func (s *seat) waitTurn(ctx context.Context) (Snapshot, bool) {
	g, env := s.g, s.g.env
	st := NewStrikes(env.BackoffBase, env.BackoffMax)
	for {
		snap, err := env.Server.Snapshot(ctx, g.serverID)
		if err != nil {
			var se *ServerError
			if ctx.Err() != nil || !errors.As(err, &se) || !se.Transient() {
				if ctx.Err() == nil {
					g.end("aborted_snapshot: "+err.Error(), none)
				}
				return Snapshot{}, false
			}
			v := st.Next(ServerErr(se.Code))
			if v.Kind == VAbort {
				g.end(ReasonAbortServer, none)
				return Snapshot{}, false
			}
			if env.sleep(ctx, v.Wait) != nil {
				return Snapshot{}, false
			}
			continue
		}
		st = NewStrikes(env.BackoffBase, env.BackoffMax)
		if snap.Outcome != outcomeOngoing {
			return snap, true
		}
		if int(snap.Turn) == s.color && g.claimTurn(s.color) {
			return snap, true
		}
		if env.sleep(ctx, env.Cfg.PollInterval) != nil {
			return Snapshot{}, false
		}
	}
}

// claimTurn marks c as acting unless the other side is still mid-turn.
func (g *Game) claimTurn(c int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.acting != none && g.acting != c {
		return false
	}
	g.acting = c
	return true
}

func (g *Game) releaseTurn(c int) {
	g.mu.Lock()
	if g.acting == c {
		g.acting = none
	}
	g.mu.Unlock()
}

func (g *Game) plies() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.replica.Plies()
}

// syncReplica brings the replica up to the server position (the opponent's
// last move) and checks the FEN.
func (g *Game) syncReplica(snap Snapshot) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.replica.FEN() == snap.FEN {
		return true
	}
	if snap.LastMove == "" || g.replica.Apply(snap.LastMove) != nil {
		return false
	}
	return g.replica.FEN() == snap.FEN
}

// endByCap closes a capped game on the server: the side to move offers, the
// other side accepts. The arena records it as a draw by cap.
func (g *Game) endByCap(s *seat, snap Snapshot) {
	ctx := context.WithoutCancel(g.runCtx)
	err := g.env.Server.Act(ctx, g.serverID, g.seatID(s.color), "draw-offer", snap.Seq, newActionID())
	if err == nil {
		err = g.env.Server.Act(ctx, g.serverID, g.seatID(s.opp()), "draw-offer/accept", snap.Seq+1, newActionID())
	}
	if err != nil {
		g.env.Log.Error("closing capped game on the server failed", "game", g.id, "err", err)
	}
	g.end(ReasonDrawPlyCap, none)
}

func (g *Game) forfeit(s *seat, reason string) {
	ctx := context.WithoutCancel(g.runCtx)
	snap, err := g.env.Server.Snapshot(ctx, g.serverID)
	if err == nil && snap.Outcome == outcomeOngoing {
		err = g.env.Server.Act(ctx, g.serverID, g.seatID(s.color), "resign", snap.Seq, newActionID())
	}
	if err != nil {
		g.env.Log.Error("resigning forfeited seat failed", "game", g.id, "err", err)
	}
	g.end(reason, s.color)
}

// turn plays one full turn. It returns false when the seat loop should stop.
func (s *seat) turn(ctx context.Context, snap Snapshot) bool {
	g := s.g
	defer g.releaseTurn(s.color)
	mode := ModeNormal
	g.mu.Lock()
	if snap.DrawOfferFrom == colorLetter[s.opp()] && g.liveOffer == s.opp() {
		mode = ModeOfferPending
	}
	g.mu.Unlock()

	res := s.decide(ctx, snap, mode)
	if res.kind == resAccepted && res.action.Kind == KindOfferDraw {
		s.lastOfferAt = s.ownMoves
		res = s.decide(ctx, snap, ModeFollowUp)
		if res.kind == resAccepted {
			s.postOffer(ctx)
		}
	}
	switch res.kind {
	case resAccepted:
		return true
	case resForfeit:
		g.forfeit(s, res.reason)
	case resAbort:
		g.end(res.reason, none)
	}
	return false
}

// postOffer sends this seat's pending draw offer after its follow-up move.
func (s *seat) postOffer(ctx context.Context) {
	g, env := s.g, s.g.env
	st := NewStrikes(env.BackoffBase, env.BackoffMax)
	for {
		snap, err := env.Server.Snapshot(ctx, g.serverID)
		if err == nil {
			if snap.Outcome != outcomeOngoing {
				return
			}
			err = env.Server.Act(ctx, g.serverID, g.seatID(s.color), "draw-offer", snap.Seq, newActionID())
		}
		if err == nil {
			g.mu.Lock()
			g.liveOffer = s.color
			g.mu.Unlock()
			return
		}
		var se *ServerError
		if ctx.Err() != nil || !errors.As(err, &se) || !se.Transient() {
			env.Log.Error("posting draw offer failed", "game", g.id, "err", err)
			return
		}
		v := st.Next(ServerErr(se.Code))
		if v.Kind == VAbort || env.sleep(ctx, v.Wait) != nil {
			env.Log.Error("posting draw offer failed", "game", g.id, "err", err)
			return
		}
	}
}

type resKind uint8

const (
	resAccepted resKind = iota + 1
	resForfeit
	resAbort
	resGameOver
	resCancelled
)

type decision struct {
	kind   resKind
	reason string
	action Action
}

func (s *seat) offerAllowed() (bool, int) {
	every := s.g.env.Cfg.DrawOfferEvery
	if s.lastOfferAt < 0 || s.ownMoves-s.lastOfferAt >= every {
		return true, 0
	}
	return false, every - (s.ownMoves - s.lastOfferAt)
}

// arenaRule checks the arena's own rules (not chess legality).
func (s *seat) arenaRule(a Action, mode ViewMode) string {
	switch mode {
	case ModeFollowUp:
		if a.Kind != KindMove {
			return "moves_only_after_offer"
		}
	case ModeOfferPending:
		if a.Kind == KindOfferDraw {
			return "offer_while_opponent_offer_pending"
		}
	default:
		if a.Kind == KindOfferDraw {
			if ok, _ := s.offerAllowed(); !ok {
				return "draw_offer_limit"
			}
		}
	}
	return ""
}

func (s *seat) view(snap Snapshot, mode ViewMode, notes []string, failuresLeft int) SeatView {
	g := s.g
	g.mu.Lock()
	san, plies := g.replica.SAN(), g.replica.Plies()
	var talk string
	if t := g.talk[s.opp()]; t.n > s.talkSeen {
		talk = t.text
	}
	g.mu.Unlock()
	ok, wait := s.offerAllowed()
	moves := append([]string(nil), snap.LegalMoves...)
	sort.Strings(moves)
	return SeatView{Mode: mode, Color: colorName[s.color], FEN: snap.FEN, Plies: plies, PlyCap: g.env.Cfg.PlyCap,
		LegalMoves: moves, SAN: san, CanClaimDraw: snap.CanClaimDraw, OfferAllowed: ok, OfferWaitMoves: wait,
		OpponentTalk: talk, Memo: s.memo, Attempts: notes, AttemptsLeft: failuresLeft}
}

// decide runs one decision to a terminal verdict and writes its record.
func (s *seat) decide(ctx context.Context, snap Snapshot, mode ViewMode) decision {
	g, env := s.g, s.g.env
	rec := DecisionRecord{RunID: env.RunID, GameID: g.id, ServerGameID: g.serverID, Seat: colorLetter[s.color],
		Player: s.p.name, Model: s.p.cfg.Model, Effort: s.p.effortLabel(), PromptPack: s.p.cfg.PromptPack,
		ViewSeq: snap.Seq, Ply: g.plies(), Mode: string(mode), AttemptLog: []AttemptLog{}}
	t0 := time.Now()
	finish := func(d decision, outcome string) decision {
		rec.Outcome = outcome
		rec.TS = time.Now().UTC()
		if s.p.cfg.IsBot() {
			rec.LatencyMS = time.Since(t0).Milliseconds()
		}
		env.Rec.Decision(rec)
		return d
	}
	st := NewStrikes(env.BackoffBase, env.BackoffMax)
	var notes []string
	var pending *Action
	var caid string
	seq := snap.Seq
	for {
		var ev Event
		var tried Action
		if pending == nil {
			a, failEv, err := s.ask(ctx, snap, mode, notes, maxFailures-st.IllegalAttempts, &rec)
			rec.Attempts++
			if err != nil {
				switch {
				case errors.Is(err, errFatal):
					return finish(decision{kind: resAbort, reason: ReasonAbortFatal}, "aborted")
				case errors.Is(err, errModelMismatch):
					return finish(decision{kind: resAbort, reason: ReasonAbortModelMismatch}, "aborted")
				case errors.Is(err, errGameBudget):
					return finish(decision{kind: resAbort, reason: ReasonAbortGameBudget}, "aborted")
				}
				return finish(decision{kind: resCancelled}, "cancelled")
			}
			if failEv != nil {
				ev = *failEv
				tried = a
			} else if code := s.arenaRule(a, mode); code != "" {
				ev, tried = Illegal(code), a
				rec.AttemptLog = append(rec.AttemptLog, AttemptLog{Kind: "illegal", Code: code, Action: a.String()})
			} else {
				pending, caid = &a, newActionID()
			}
		}
		if pending != nil {
			tried = *pending
			var over bool
			ev, over = s.submit(ctx, *pending, seq, caid)
			if over {
				return finish(decision{kind: resGameOver}, "game_over")
			}
			if ctx.Err() != nil {
				return finish(decision{kind: resCancelled}, "cancelled")
			}
			if ev.Kind != EvAccepted {
				rec.AttemptLog = append(rec.AttemptLog, AttemptLog{Kind: eventName(ev.Kind), Code: ev.Code, Action: tried.String()})
			}
		}
		v := st.Next(ev)
		rec.IllegalAttempts, rec.MaxTokensStops, rec.Timeouts = st.IllegalAttempts, st.MaxTokensStops, st.TotalTimeouts
		rec.ProviderRetries, rec.ServerRetries, rec.StaleResends = st.ProviderRetries, st.ServerRetries, st.StaleResends
		switch v.Kind {
		case VDone:
			s.applyAccepted(ctx, *pending, mode, &rec)
			return finish(decision{kind: resAccepted, action: *pending}, "accepted")
		case VAsk:
			pending = nil
			if n := describeFailure(ev, tried, env.Cfg.DecisionTimeout); n != "" {
				notes = append(notes, n)
			}
			if env.sleep(ctx, v.Wait) != nil {
				return finish(decision{kind: resCancelled}, "cancelled")
			}
		case VResend:
			if env.sleep(ctx, v.Wait) != nil {
				return finish(decision{kind: resCancelled}, "cancelled")
			}
			if ev.Kind == EvStale {
				fresh, err := env.Server.Snapshot(ctx, g.serverID)
				if err == nil {
					if fresh.Outcome != outcomeOngoing {
						return finish(decision{kind: resGameOver}, "game_over")
					}
					seq = fresh.Seq
				}
			}
		case VForfeit:
			env.Log.Warn("forfeit", "game", g.id, "seat", colorLetter[s.color], "player", s.p.name, "reason", v.Reason)
			return finish(decision{kind: resForfeit, reason: v.Reason}, "forfeit")
		case VAbort:
			env.Log.Warn("abort", "game", g.id, "seat", colorLetter[s.color], "reason", v.Reason)
			return finish(decision{kind: resAbort, reason: v.Reason}, "aborted")
		}
	}
}

var (
	errFatal         = errors.New("fatal model error")
	errModelMismatch = errors.New("response model mismatch")
	errGameBudget    = errors.New("game budget exceeded")
)

// ask gets one action from the seat's player. failEv is non-nil when the
// attempt failed before reaching the server. err is non-nil only for
// cancellation or a fatal API error.
func (s *seat) ask(ctx context.Context, snap Snapshot, mode ViewMode, notes []string, left int, rec *DecisionRecord) (Action, *Event, error) {
	if s.p.cfg.IsBot() {
		moves := append([]string(nil), snap.LegalMoves...)
		sort.Strings(moves)
		if len(moves) == 0 {
			return Action{Kind: KindResign}, nil, nil
		}
		uci := moves[s.rng.IntN(len(moves))]
		rec.AttemptLog = append(rec.AttemptLog, AttemptLog{Kind: "answered", Action: uci})
		return Action{Kind: KindMove, UCI: uci}, nil, nil
	}
	g, env := s.g, s.g.env
	user, err := s.p.pack.User(s.view(snap, mode, notes, left))
	if err != nil {
		return Action{}, nil, fmt.Errorf("%w: prompt: %v", errFatal, err)
	}
	req := ModelRequest{Model: s.p.cfg.Model, Effort: s.p.cfg.Effort, Thinking: s.p.cfg.Thinking,
		MaxTokens: s.p.cfg.MaxTokens, Tool: s.p.pack.Tool, System: s.p.system, User: user}
	dctx, cancel := context.WithTimeout(ctx, env.Cfg.DecisionTimeout)
	t0 := time.Now()
	reply, err := env.Model.Decide(dctx, req)
	cancel()
	lat := time.Since(t0).Milliseconds()
	rec.LatencyMS += lat
	al := AttemptLog{LatencyMS: lat}
	fail := func(e Event) (Action, *Event, error) {
		al.Kind, al.Code = eventName(e.Kind), e.Code
		rec.AttemptLog = append(rec.AttemptLog, al)
		return Action{}, &e, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return Action{}, nil, ctx.Err()
		}
		var me *ModelError
		if !errors.As(err, &me) {
			return fail(ProviderErr("unknown"))
		}
		switch me.Kind {
		case ModelTimeout:
			return fail(Timeout())
		case ModelFatal:
			al.Kind, al.Code = "fatal", me.Code
			rec.AttemptLog = append(rec.AttemptLog, al)
			env.Log.Error("fatal API error; stopping the run", "game", g.id, "status", me.Status, "code", me.Code)
			if env.OnFatal != nil {
				env.OnFatal(me)
			}
			return Action{}, nil, errFatal
		default:
			return fail(ProviderErr(me.Code))
		}
	}
	usd := s.p.price.Cost(reply.Usage)
	env.Budget.Charge(g.id, usd)
	g.mu.Lock()
	g.usd += usd
	gameUSD := g.usd
	prior := g.modelCalls[req.Model]
	g.modelCalls[req.Model]++
	g.mu.Unlock()
	rec.Tokens = rec.Tokens.Add(reply.Usage)
	rec.USD += usd
	al.Tokens, al.USD, al.StopReason, al.ResponseModel = reply.Usage, usd, reply.StopReason, reply.Model
	if prior > 0 && reply.Usage.CacheRead == 0 {
		rec.CacheMissAfterFirst = true
		env.Log.Warn("cache_read_input_tokens is 0 after the game's first call", "game", g.id, "model", req.Model, "ply", rec.Ply)
	}
	if ceiling := g.ceiling(); ceiling > 0 && gameUSD > ceiling {
		al.Kind, al.Code = "aborted", "game_budget"
		rec.AttemptLog = append(rec.AttemptLog, al)
		env.Log.Warn("game spend over its ceiling; aborting the game", "game", g.id,
			"usd", fmt.Sprintf("%.4f", gameUSD), "ceiling", fmt.Sprintf("%.4f", ceiling))
		return Action{}, nil, errGameBudget
	}
	if reply.Model != req.Model {
		al.Kind, al.Code = "fatal", "model_mismatch"
		rec.AttemptLog = append(rec.AttemptLog, al)
		env.Log.Error("response model differs from requested model; stopping the run", "game", g.id, "requested", req.Model, "got", reply.Model)
		if env.OnFatal != nil {
			env.OnFatal(fmt.Errorf("response model %q differs from requested model %q", reply.Model, req.Model))
		}
		return Action{}, nil, errModelMismatch
	}
	if reply.Act == nil {
		switch reply.StopReason {
		case "max_tokens":
			return fail(MaxTokens())
		case "refusal":
			return fail(Malformed("refusal"))
		}
		return fail(Malformed("no_act"))
	}
	a, perr := ParseAction(reply.Act.Action, reply.Act.Talk, reply.Act.Memo)
	if perr != nil {
		var me *MalformedError
		errors.As(perr, &me)
		al.Action = reply.Act.Action
		e := Malformed(me.Code)
		al.Kind, al.Code = eventName(e.Kind), e.Code
		rec.AttemptLog = append(rec.AttemptLog, al)
		return a, &e, nil
	}
	al.Kind, al.Action = "answered", a.String()
	rec.AttemptLog = append(rec.AttemptLog, al)
	return a, nil, nil
}

// submit sends a to the server. over is true when the game had already ended.
func (s *seat) submit(ctx context.Context, a Action, seq uint64, caid string) (ev Event, over bool) {
	g := s.g
	srv, id, me := g.env.Server, g.serverID, g.seatID(s.color)
	var err error
	switch a.Kind {
	case KindOfferDraw:
		return Accepted(), false // posted after the follow-up move
	case KindMove:
		err = srv.Move(ctx, id, me, seq, a.UCI, a.Claim, caid)
	case KindClaimDraw:
		err = srv.Act(ctx, id, me, "claim-draw", seq, caid)
	case KindResign:
		err = srv.Act(ctx, id, me, "resign", seq, caid)
	case KindAcceptDraw:
		err = srv.Act(ctx, id, me, "draw-offer/accept", seq, caid)
	}
	if err == nil {
		return Accepted(), false
	}
	var se *ServerError
	if !errors.As(err, &se) {
		return ServerErr("network"), false
	}
	switch se.Code {
	case "game_over", "flagged":
		return Event{}, true
	case "stale_sequence", "not_your_turn":
		return Stale(), false
	case "bad_move_syntax", "invalid_request":
		return Malformed(se.Code), false
	}
	if se.Transient() {
		return ServerErr(se.Code), false
	}
	return Illegal(se.Code), false
}

func (s *seat) applyAccepted(ctx context.Context, a Action, mode ViewMode, rec *DecisionRecord) {
	g := s.g
	rec.Action, rec.Talk, rec.MemoLen = a.String(), a.Talk, len([]rune(a.Memo))
	s.memo = a.Memo
	g.mu.Lock()
	ply := g.replica.Plies()
	if t := g.talk[s.opp()]; t.n > s.talkSeen {
		s.talkSeen = t.n
	}
	if a.Talk != "" {
		g.talk[s.color] = talkMsg{text: a.Talk, n: g.talk[s.color].n + 1}
	}
	if a.Memo != "" {
		g.memos = append(g.memos, MemoEntry{Ply: ply, Seat: colorLetter[s.color], Memo: a.Memo})
	}
	g.actions = append(g.actions, ActionLogEntry{Ply: ply, Seat: colorLetter[s.color], Action: a.String(), Talk: a.Talk})
	if a.Kind == KindMove {
		if err := g.replica.Apply(a.UCI); err != nil {
			g.env.Log.Error("server accepted a move the replica rejects", "game", g.id, "move", a.UCI)
		}
		s.ownMoves++
		if mode == ModeOfferPending {
			g.liveOffer = none // a move declines the opponent's offer
		}
	}
	g.mu.Unlock()
	if a.Kind == KindMove && a.Claim {
		if snap, err := g.env.Server.Snapshot(ctx, g.serverID); err == nil && snap.Outcome == outcomeOngoing {
			rec.Note = "claim_no_effect"
		}
	}
}

func eventName(k EventKind) string {
	return map[EventKind]string{EvAccepted: "accepted", EvIllegal: "illegal", EvMalformed: "malformed",
		EvMaxTokens: "max_tokens", EvTimeout: "timeout", EvProvider: "provider_error",
		EvServerErr: "server_error", EvStale: "stale"}[k]
}

// describeFailure phrases a failed attempt for the seat's next request.
func describeFailure(ev Event, a Action, timeout time.Duration) string {
	act := a.String()
	switch ev.Kind {
	case EvTimeout:
		return fmt.Sprintf("no answer within %d seconds (timeout). A second timeout in a row forfeits; answer promptly.", int(timeout.Seconds()))
	case EvMaxTokens:
		return "your reply hit the output token limit before calling act (max_tokens). Think less and call act."
	case EvMalformed:
		switch ev.Code {
		case "no_act":
			return "no act call (no_act): your reply did not call the act tool"
		case "refusal":
			return "your reply was declined (refusal) and contained no act call"
		case "talk_too_long":
			return fmt.Sprintf("talk_too_long: talk must be at most %d characters", MaxTalkRunes)
		case "memo_too_long":
			return fmt.Sprintf("memo_too_long: memo must be at most %d characters", MaxMemoRunes)
		case "bad_action":
			return "your action is not in the action grammar (bad_action)"
		}
		return fmt.Sprintf("`%s` was malformed (%s)", act, ev.Code)
	case EvIllegal:
		reason := map[string]string{
			"illegal_move":                       "not a legal move in this position",
			"no_pending_offer":                   "there is no draw offer from your opponent to accept",
			"own_offer":                          "you cannot accept your own draw offer",
			"no_claimable_draw":                  "there is no threefold repetition or 50-move basis for a claim now",
			"draw_offer_limit":                   "you offered a draw too recently",
			"moves_only_after_offer":             "after offer_draw you must play a move",
			"offer_while_opponent_offer_pending": "your opponent has offered a draw: accept it, play a move (which declines), claim a draw or resign",
		}[ev.Code]
		if reason == "" {
			reason = "rejected by the server"
		}
		return fmt.Sprintf("`%s` was rejected (%s): %s", act, ev.Code, reason)
	}
	return ""
}
