.PHONY: all build run test lint docker-up docker-down migrate tidy

# ── Config ────────────────────────────────────────────────────────────────────
BINARY        := server
BUILD_DIR     := ./bin
MAIN          := ./cmd/server
DATABASE_URL  ?= postgres://orchestrator:orchestrator@localhost:5432/orchestrator

# ── Build ─────────────────────────────────────────────────────────────────────
all: build

build:
	@echo "→ Building $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY) $(MAIN)

run: build
	@echo "→ Starting server..."
	LLM_MODE=mock DATABASE_URL=$(DATABASE_URL) $(BUILD_DIR)/$(BINARY)

# ── Dev infra ────────────────────────────────────────────────────────────────
docker-up:
	docker compose up -d postgres redis
	@echo "→ Waiting for services..."
	@sleep 2

docker-down:
	docker compose down -v

# ── Database ──────────────────────────────────────────────────────────────────
migrate:
	@echo "→ Running migrations against $(DATABASE_URL)..."
	psql "$(DATABASE_URL)" -f migrations/001_init.sql

# ── Quality ───────────────────────────────────────────────────────────────────
tidy:
	go mod tidy

lint:
	golangci-lint run ./...

test:
	go test ./... -v -race -count=1

# ── Convenience ───────────────────────────────────────────────────────────────
# Quick smoke-test: create a workflow against the running server.
smoke:
	curl -s -X POST http://localhost:8080/api/v1/workflows \
	  -H "Content-Type: application/json" \
	  -d '{"task":"Research and summarize the key benefits of Go for backend services, then write a short blog post about it."}' | jq .