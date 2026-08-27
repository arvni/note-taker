// Command server hosts the web UI, OAuth flow, dashboards, and API (spec §8, §46-47).
// This is a scaffold entrypoint; handlers land in later phases.
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		// TODO(P1-2): check DB + Redis connectivity.
		fmt.Fprintln(w, "ready")
	})

	log.Printf("server listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
	log.Fatal(http.ListenAndServe(cfg.HTTPAddr, mux))
}
