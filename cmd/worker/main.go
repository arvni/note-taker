// Command worker runs background jobs: directory sync, token refresh, and
// calendar synchronization (spec §20, §15-16, §42). Scaffold entrypoint.
package main

import (
	"log"

	"github.com/arvinizadi/fathom/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("worker started (env=%s). Jobs are registered in later phases.", cfg.AppEnv)
	// TODO(P2-4): directory sync scheduler
	// TODO(P5-1): token refresh loop with Redis lock
	// TODO(P8-4): calendar sync loop
	select {}
}
