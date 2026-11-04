// Command arena plays chess matches between Claude models and baseline bots
// against a running chess server (cmd/server), over HTTP only.
//
//	ARENA_ANTHROPIC_API_KEY=... go run ./cmd/arena -config arena.gate.yaml
//
// The provider key is read only from ARENA_ANTHROPIC_API_KEY.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"kisschess/internal/arena"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		cfgPath = flag.String("config", "arena.yaml", "match file (YAML)")
		runID   = flag.String("run-id", "", "run id (default: UTC timestamp)")
		outDir  = flag.String("out", "", "results root (default: out_dir from the match file)")
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := arena.LoadConfig(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		return 2
	}
	if *outDir != "" {
		cfg.OutDir = *outDir
	}
	key := os.Getenv(arena.KeyEnv)
	if err := cfg.Validate(key != ""); err != nil {
		log.Error("config", "err", err)
		return 2
	}
	var model arena.ModelClient
	if key != "" {
		model = arena.NewAnthropicClient(key, "")
	}
	key = ""

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sum, err := arena.RunMatch(ctx, cfg, model, arena.RunOptions{RunID: *runID, Log: log})
	if err != nil {
		log.Error("run", "err", err)
		return 1
	}
	fmt.Print(arena.FormatRunSummary(sum))
	fmt.Printf("\nresults: %s\n", filepath.Join(cfg.OutDir, sum.RunID))
	if sum.FatalError != "" {
		return 1
	}
	return 0
}
