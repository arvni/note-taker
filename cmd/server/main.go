// Command server hosts the web UI, OAuth flow, dashboards, and API (spec §8, §46-47).
// It also runs DB migrations via subcommands:
//
//	server migrate up      apply all pending migrations
//	server migrate down    roll back the most recent migration
//
// With no subcommand it starts the HTTP server.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(cfg, os.Args[2:])
		return
	}

	serve(cfg)
}

func runMigrate(cfg *config.Config, args []string) {
	if len(args) == 0 {
		log.Fatal("usage: server migrate [up|down]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	defer pool.Close()

	switch args[0] {
	case "up":
		n, err := db.MigrateUp(ctx, pool)
		if err != nil {
			log.Fatalf("migrate up: %v", err)
		}
		log.Printf("migrate up: applied %d migration(s)", n)
	case "down":
		v, err := db.MigrateDown(ctx, pool)
		if err != nil {
			log.Fatalf("migrate down: %v", err)
		}
		if v == 0 {
			log.Print("migrate down: nothing to roll back")
		} else {
			log.Printf("migrate down: rolled back version %d", v)
		}
	default:
		log.Fatalf("migrate: unknown subcommand %q (want up|down)", args[0])
	}
}

func serve(cfg *config.Config) {
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("server: db: %v", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})

	log.Printf("server listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
	log.Fatal(http.ListenAndServe(cfg.HTTPAddr, mux))
}
