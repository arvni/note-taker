.PHONY: help up down deps logs server worker poc migrate migrate-down test tidy build fmt vet spa prod-up prod-down prod-migrate

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'

## ── Local run (full stack in Docker; app on :8080) ──
up:            ## build + run everything (postgres, redis, migrate, server, worker)
	docker compose up -d --build
down:          ## stop the local stack
	docker compose down
deps:          ## just postgres + redis (for running the app via `go run` on the host)
	docker compose up -d postgres redis
logs:          ## tail server + worker logs
	docker compose logs -f server worker

## ── Host dev (run binaries directly; needs `make deps` + a .env) ──
server:        ## run the HTTP server (serves API + embedded SPA at /app/)
	go run ./cmd/server
worker:        ## run background jobs
	go run ./cmd/worker
poc:           ## run the Zoho POC (spec §58)
	go run ./cmd/poc
migrate:       ## apply DB migrations
	go run ./cmd/server migrate up
migrate-down:  ## roll back the last migration
	go run ./cmd/server migrate down

## ── Frontend ──
spa:           ## build the React admin SPA into web/spa/dist
	cd web/spa && npm ci && npm run build

## ── Quality ──
test:
	go test ./...
tidy:
	go mod tidy
fmt:
	go fmt ./... && cd web/spa && npm run build --silent >/dev/null 2>&1 || true
vet:
	go vet ./...
build:
	go build -o bin/server ./cmd/server
	go build -o bin/worker ./cmd/worker

## ── Production (Caddy TLS; uses .env.prod) ──
prod-migrate:  ## apply migrations in production
	docker compose -f docker-compose.prod.yml run --rm migrate
prod-up:       ## start the production stack
	docker compose -f docker-compose.prod.yml up -d --build
prod-down:     ## stop the production stack
	docker compose -f docker-compose.prod.yml down
