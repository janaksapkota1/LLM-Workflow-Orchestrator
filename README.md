# LLM Workflow Orchestrator

A production-grade Go backend that accepts a user task, decomposes it into multi-step workflows using an LLM, executes each step asynchronously with retry logic, and returns the final synthesized result.

```
Client → API Server → Orchestrator → Queue → Workers → LLM API
                           ↓
                      PostgreSQL
                           ↓
                         Redis
```

---

## Architecture

| Layer | Package | Responsibility |
|---|---|---|
| HTTP | `internal/api` | REST handlers, request validation, response formatting |
| Orchestration | `internal/orchestrator` | Task decomposition, step sequencing, workflow lifecycle |
| Queueing | `internal/queue` | Redis FIFO queue + sorted-set retry queue + dead-letter queue |
| Execution | `internal/worker` | Concurrent workers, LLM calls, retry/backoff, state updates |
| LLM Client | `internal/llm` | Anthropic Messages API wrapper (single and multi-turn) |
| Persistence | `internal/store` | PostgreSQL CRUD via pgx/v5 connection pool |
| Config | `internal/config` | Env-var loading with sensible defaults |
| Logging | `internal/logger` | Zerolog structured logging (console + JSON modes) |

---

## Workflow Lifecycle

```
POST /api/v1/workflows
        │
        ▼
[Workflow] status=pending  ──── async goroutine ────►  LLM decomposes task
                                                               │
                                                               ▼
                                                   [Steps] status=pending
                                                               │
                                                   [Workflow] status=running
                                                               │
                                                    Enqueue step[0]
                                                               │
        ┌──────────────────────────────────────────────────────┘
        ▼
 Worker dequeues job
        │
        ├─ LLM executes step prompt  ──► success ──► step=completed
        │                                                  │
        │                              AdvanceWorkflow()   │
        │                                   ├─ more steps? ──► enqueue step[n+1]
        │                                   └─ done?       ──► workflow=completed
        │
        └─ LLM error ──► retry < maxRetries ──► EnqueueRetry (exponential backoff)
                     └─ exhausted            ──► step=failed, workflow=failed, dead-letter
```

---

## Quick Start

### 1. Prerequisites

- Go 1.22+
- Docker & Docker Compose
- An Anthropic API key

### 2. Setup

```bash
git clone https://github.com/yourorg/llm-orchestrator
cd llm-orchestrator

cp .env.example .env
# Edit .env — at minimum set ANTHROPIC_API_KEY

# Start Postgres and Redis
make docker-up

# Run DB migrations
make migrate

# Start the server
make run
```

### 3. Smoke test

```bash
make smoke
```

Or manually:

```bash
# Create a workflow
curl -X POST http://localhost:8080/api/v1/workflows \
  -H "Content-Type: application/json" \
  -d '{"task": "Research the top 3 programming languages for AI development and write a comparison report."}'

# Poll for completion (replace <id> with the returned workflow ID)
curl http://localhost:8080/api/v1/workflows/<id>

# Check individual steps
curl http://localhost:8080/api/v1/workflows/<id>/steps
```

---

## API Reference

### `POST /api/v1/workflows`

Create and start a new workflow.

**Request body:**
```json
{
  "task": "Your multi-step task description",
  "metadata": { "optional": "key-value pairs" }
}
```

**Response:** `201 Created` — the created `Workflow` object.

---

### `GET /api/v1/workflows`

List workflows (paginated).

**Query params:** `limit` (default 20), `offset` (default 0).

---

### `GET /api/v1/workflows/{id}`

Get a workflow and all its steps.

---

### `GET /api/v1/workflows/{id}/steps`

Get only the steps for a workflow.

---

### `DELETE /api/v1/workflows/{id}`

Cancel a workflow.

---

### `GET /api/v1/queue/depth`

Returns the number of jobs currently in the main queue.

---

## Configuration

All configuration is via environment variables (or `.env` file):

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | — | PostgreSQL DSN (**required**) |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | — | Redis password (optional) |
| `REDIS_DB` | `0` | Redis DB index |
| `ANTHROPIC_API_KEY` | — | Anthropic API key (**required**) |
| `LLM_MODEL` | `claude-sonnet-4-20250514` | Model to use |
| `LLM_MAX_TOKENS` | `4096` | Max tokens per LLM call |
| `WORKER_CONCURRENCY` | `5` | Number of concurrent workers |
| `MAX_RETRIES` | `3` | Max retries per step before dead-lettering |
| `LOG_FORMAT` | `console` | `json` for structured output |

---

## Retry & Backoff Strategy

Failed steps are retried with exponential backoff via a Redis sorted set:

```
attempt 1 → wait  2s
attempt 2 → wait  4s
attempt 3 → wait  8s
attempt N → wait min(2^N, 300)s
```

After `MAX_RETRIES` attempts, the job is moved to the `llm:jobs:dead` dead-letter list and the workflow is marked `failed`.

A background loop inside each worker promotes due retry jobs back to the main queue every polling cycle.

---

## Project Structure

```
llm-orchestrator/
├── cmd/server/          # main.go — wires all dependencies
├── internal/
│   ├── api/             # HTTP handlers (chi router)
│   ├── orchestrator/    # task decomposition + workflow advancement
│   ├── worker/          # async workers + worker pool
│   ├── llm/             # Anthropic API client
│   ├── models/          # shared structs (Workflow, WorkflowStep, JobMessage)
│   ├── store/           # PostgreSQL queries (pgx/v5)
│   ├── queue/           # Redis queue operations
│   ├── config/          # env-var config loader
│   └── logger/          # zerolog setup
├── migrations/          # SQL schema
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── .env.example
```

---

## Production Checklist

- [ ] Set `LOG_FORMAT=json` and ship logs to your aggregator
- [ ] Configure Postgres connection pool limits (`pgxpool.Config`)
- [ ] Add a `/metrics` endpoint (Prometheus) for queue depth, step latency, retry rate
- [ ] Mount persistent volumes in docker-compose for Postgres and Redis
- [ ] Add authentication middleware to the API
- [ ] Set up alerts on dead-letter queue growth
- [ ] Consider a distributed lock (Redis `SET NX`) for exactly-once step execution