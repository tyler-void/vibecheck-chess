// Package http wires the kisschess HTTP surface: lobby + game routes,
// /healthz, /readyz, /metrics, and the SSE subscribe endpoint. This file
// holds the skeleton; the game and SSE handlers land in Phase 3 (T046–T051).
//
// Server is a thin facade around stdlib net/http (Constitution I — no
// framework). It composes a mux, a GameManager, and a readiness flag.
package http

import (
	"log/slog"
	stdhttp "net/http"
	"sync/atomic"
	"time"

	"kisschess/internal/chess"
	"kisschess/internal/sse"
)

// Server is the kisschess HTTP entry point. Fields are exported only where
// other packages need them (cmd/server/main.go in particular).
type Server struct {
	Mux        *stdhttp.ServeMux
	Manager    *chess.GameManager
	Logger     *slog.Logger
	ready      atomic.Bool
	staticRoot string
}

// Options configures a new Server.
type Options struct {
	Manager    *chess.GameManager
	Logger     *slog.Logger
	StaticRoot string // filesystem path to web/ (served at /static/)
}

// NewServer constructs the server with /healthz, /readyz, /metrics, and the
// /static/ file server wired up. Phase 3 will register the game / SSE
// routes. The server starts in not-ready state (use MarkReady once
// dependencies are initialised) so the LB can hold traffic during boot.
func NewServer(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{
		Mux:        stdhttp.NewServeMux(),
		Manager:    opts.Manager,
		Logger:     opts.Logger,
		staticRoot: opts.StaticRoot,
	}
	s.routes()
	return s
}

// MarkReady transitions the server from "warming up" to "accepting traffic".
// Call exactly once after the manager + dependencies are initialised but
// before binding the listener.
func (s *Server) MarkReady() { s.ready.Store(true) }

// MarkNotReady is called at the start of graceful shutdown so the LB stops
// routing new traffic while in-flight requests drain.
func (s *Server) MarkNotReady() { s.ready.Store(false) }

func (s *Server) routes() {
	s.Mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.Mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.Mux.HandleFunc("GET /metrics", s.handleMetrics) // populated by T085 in Phase 9

	// Game CRUD + state.
	s.Mux.HandleFunc("POST /games", s.handleCreateGame)
	s.Mux.HandleFunc("GET /games/{id}/snapshot", s.handleSnapshot)
	s.Mux.HandleFunc("POST /games/{id}/move", s.handleMove)

	// SSE subscribe + initial hydration.
	s.Mux.HandleFunc("GET /games/{id}/sse", s.handleSSE)

	// Voluntary game-termination paths (FR-036 / FR-037).
	s.Mux.HandleFunc("POST /games/{id}/resign", s.handleResign)
	s.Mux.HandleFunc("POST /games/{id}/draw-offer/accept", s.handleDrawAccept) // more specific first
	s.Mux.HandleFunc("POST /games/{id}/draw-offer", s.handleDrawOffer)
	s.Mux.HandleFunc("POST /games/{id}/claim-draw", s.handleDrawClaim)

	if s.staticRoot != "" {
		fs := stdhttp.FileServer(stdhttp.Dir(s.staticRoot))
		s.Mux.Handle("GET /static/", stdhttp.StripPrefix("/static/", fs))

		// Server-rendered Datastar pages. lobby.html + game.html live
		// under staticRoot; templates load lazily on first request.
		s.Mux.HandleFunc("GET /{$}", s.handleLobby)
		s.Mux.HandleFunc("GET /game/{id}", s.handleGamePage)
	}
}

// ServeHTTP makes Server an http.Handler so cmd/server can drop it straight
// into http.Server.Handler without wrapping.
func (s *Server) ServeHTTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	s.Mux.ServeHTTP(w, r)
}

// handleHealthz is liveness — always 200 while the process is up.
func (s *Server) handleHealthz(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(stdhttp.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz is readiness — 200 when the server is accepting traffic,
// 503 during boot or graceful shutdown.
func (s *Server) handleReadyz(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.ready.Load() {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
		return
	}
	w.WriteHeader(stdhttp.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready\n"))
}

// handleMetrics serves the Prometheus exposition from our Registry
// (Phase 9 T085). Replaces the Phase 2 placeholder.
func (s *Server) handleMetrics(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	// Update gauges that need a fresh read at scrape time.
	if s.Manager != nil {
		MetricGamesActive.Set(float64(s.Manager.Count()))
	}
	MetricsHandler().ServeHTTP(w, r)
}

// handleSSE dispatches GET /games/{id}/sse to the per-room subscribe
// handler. Looks up the room then delegates to sse.SubscribeHandler, which
// owns the SSE lifecycle (headers, replay, hub register/range/close, ping
// ticker, Last-Event-ID handling).
func (s *Server) handleSSE(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := chess.GameID(r.PathValue("id"))
	room, ok := s.Manager.Get(id)
	if !ok {
		stdhttp.NotFound(w, r)
		return
	}
	sse.SubscribeHandler(roomAdapter{room}, time.Now).ServeHTTP(w, r)
}

// roomAdapter bridges *chess.GameRoom to the sse.Room interface so internal/sse
// doesn't have to import internal/chess (and break the package layering).
type roomAdapter struct{ inner *chess.GameRoom }

func (a roomAdapter) Hub() *sse.FanoutHub { return a.inner.Hub() }
func (a roomAdapter) ReplayOrSnapshot(lastSeq uint64, now time.Time) ([][]byte, []byte) {
	return a.inner.ReplayOrSnapshot(lastSeq, now)
}
func (a roomAdapter) HydrateBytes(now time.Time) []byte {
	return a.inner.HydrateBytes(now)
}
