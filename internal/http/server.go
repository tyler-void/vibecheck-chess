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

	"kisschess/internal/chess"
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

	if s.staticRoot != "" {
		fs := stdhttp.FileServer(stdhttp.Dir(s.staticRoot))
		s.Mux.Handle("GET /static/", stdhttp.StripPrefix("/static/", fs))
	}

	// Phase 3 will register here:
	//   POST   /games
	//   GET    /games/{id}/snapshot
	//   POST   /games/{id}/move
	//   POST   /games/{id}/resign       (T104 / FR-036)
	//   POST   /games/{id}/draw-offer   (T104 / FR-037)
	//   POST   /games/{id}/draw-offer/accept (T104 / FR-037)
	//   GET    /games/{id}/sse
	//   GET    /                         (lobby)
	//   GET    /game/{id}                (single-game view)
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

// handleMetrics is a placeholder that Phase 9 (T085) will replace with the
// real Prometheus exposition. Returning an empty 200 here keeps scrapers
// happy while the registry is being built.
func (s *Server) handleMetrics(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(stdhttp.StatusOK)
}
