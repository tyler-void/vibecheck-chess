// Command server is the kisschess single-node binary. One process owns the
// authoritative state of all its routed games (Constitution II); horizontal
// scale is sticky-routing across N processes, not shared state.
//
// Run with:
//
//	go run ./cmd/server
//	go run ./cmd/server -addr=:9090
//	./bin/kisschess -addr=:8080 -static=./web
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	stdhttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kisschess/internal/chess"
	"kisschess/internal/http"
)

func main() {
	var (
		addr            = flag.String("addr", ":8080", "TCP listen address")
		staticRoot      = flag.String("static", "./web", "Filesystem path served at /static/")
		shutdownGrace   = flag.Duration("shutdown-grace", 5*time.Second, "Maximum time to drain in-flight requests on SIGINT")
		readHeaderTime  = flag.Duration("read-header-timeout", 5*time.Second, "Per-request read-header deadline (mitigates Slowloris)")
		writeTimeout    = flag.Duration("write-timeout", 0, "Per-request write deadline; 0 = no deadline (SSE streams need this)")
		logLevel        = flag.String("log-level", "info", "Log verbosity: debug | info | warn | error")
		_               = writeTimeout // SSE handlers use ResponseController per-write deadlines instead
	)
	flag.Parse()

	logger := newLogger(*logLevel)

	manager := chess.NewGameManager()
	srv := http.NewServer(http.Options{
		Manager:    manager,
		Logger:     logger,
		StaticRoot: *staticRoot,
	})

	httpServer := &stdhttp.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: *readHeaderTime,
		// NB: do NOT set WriteTimeout — SSE handlers stream indefinitely and
		// rely on per-write deadlines via http.NewResponseController (T077).
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenErr := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", *addr), slog.String("static_root", *staticRoot))
		srv.MarkReady()
		if err := httpServer.ListenAndServe(); err != nil && err != stdhttp.ErrServerClosed {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case err := <-listenErr:
		if err != nil {
			logger.Error("listen failed", slog.String("err", err.Error()))
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining", slog.Duration("grace", *shutdownGrace))
		srv.MarkNotReady()

		drainCtx, cancel := context.WithTimeout(context.Background(), *shutdownGrace)
		defer cancel()
		if err := httpServer.Shutdown(drainCtx); err != nil {
			logger.Warn("forced shutdown after grace expired", slog.String("err", err.Error()))
		}
		logger.Info("shutdown complete")
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}
