// Command worker runs background jobs: directory sync (spec §20), token refresh
// (spec §15-16), and calendar synchronization (spec §42). It shuts down
// gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("worker: db: %v", err)
	}
	defer pool.Close()

	log.Printf("worker started (env=%s); directory sync interval=%s", cfg.AppEnv, cfg.DirectorySyncInterval)

	// Directory sync loop (spec §20). The concrete per-org source + token wiring
	// is added once the org-level auth model (spec §54) is resolved; until then
	// this is a heartbeat that proves scheduling and shutdown work.
	directory.RunPeriodic(ctx, cfg.DirectorySyncInterval, func(ctx context.Context) error {
		log.Print("directory sync tick: no directory source configured yet (pending §54 auth model)")
		return nil
	})

	// TODO(P5-1): token refresh loop with Redis lock
	// TODO(P8-4): calendar sync loop

	log.Print("worker stopped")
}
