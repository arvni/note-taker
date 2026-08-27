.PHONY: up down server worker poc migrate migrate-down test tidy build fmt vet

up:            ## start postgres + redis
	docker compose up -d

down:          ## stop deps
	docker compose down

server:        ## run the HTTP server
	go run ./cmd/server

worker:        ## run background worker
	go run ./cmd/worker

poc:           ## run the Zoho POC (spec §58)
	go run ./cmd/poc

migrate:       ## apply DB migrations
	go run ./cmd/server migrate up

migrate-down:  ## roll back last migration
	go run ./cmd/server migrate down

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

build:
	go build -o bin/server ./cmd/server
	go build -o bin/worker ./cmd/worker
	go build -o bin/poc ./cmd/poc
