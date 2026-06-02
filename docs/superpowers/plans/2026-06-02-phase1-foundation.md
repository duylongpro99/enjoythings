# Phase 1 Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the runnable Phase 1 Go services foundation described in `services/docs/phase1/specs/phase1-foundation.md`.

**Architecture:** A single Go API binary wires config, pgx pool lifecycle, and HTTP routing. Config, handlers, repo/database utilities, and service/router setup live in separate internal packages so future wallet, transfer, ledger, and auth specs can extend them without breaking boundaries.

**Tech Stack:** Go 1.26, standard `net/http`, `pgx/v5`, `golang-migrate` SQL migration layout, `sqlc`, Docker Compose with Postgres 16.

---

### Task 1: Config

**Files:**
- Create: `services/internal/config/config.go`
- Test: `services/internal/config/config_test.go`

- [ ] Write tests covering defaults, required `DATABASE_URL` and `JWT_SECRET`, invalid `DB_MAX_CONNS`, and custom values.
- [ ] Run `go test ./internal/config` and confirm the tests fail because the package does not exist.
- [ ] Implement `Load()` and `LoadFromLookup()` with env parsing and validation.
- [ ] Run `go test ./internal/config` and confirm the package passes.

### Task 2: Health Handlers

**Files:**
- Create: `services/internal/handler/response.go`
- Create: `services/internal/handler/health.go`
- Test: `services/internal/handler/health_test.go`

- [ ] Write handler tests for `/healthz`, ready success, and ready failure using a fake readiness checker.
- [ ] Run `go test ./internal/handler` and confirm the tests fail because handlers are missing.
- [ ] Implement JSON success and error envelopes plus `Health` and `Readiness` handlers.
- [ ] Run `go test ./internal/handler` and confirm the package passes.

### Task 3: Runtime Wiring

**Files:**
- Create: `services/go.mod`
- Create: `services/cmd/api/main.go`
- Create: `services/internal/repo/db.go`
- Create: `services/internal/service/router.go`
- Create: placeholder package files under `services/internal/domain` and `services/internal/middleware`

- [ ] Implement pgx pool creation, ping, and close behavior.
- [ ] Implement a service router that registers `/healthz` and `/readyz`.
- [ ] Implement `main.go` so startup loads config, connects to Postgres, logs non-secret metadata, and serves HTTP.
- [ ] Run `go test ./...` and fix compile or behavior failures.

### Task 4: SQL and Docker

**Files:**
- Create: `services/db/migrations/000001_enable_pgcrypto.up.sql`
- Create: `services/db/migrations/000001_enable_pgcrypto.down.sql`
- Create: `services/db/query/health.sql`
- Create: `services/sqlc.yaml`
- Create: `services/Dockerfile`
- Create: `services/docker-compose.yml`
- Modify: `.env.example`

- [ ] Add the pgcrypto migration and reversible down migration.
- [ ] Add a minimal sqlc query and config so `sqlc generate` has committed inputs.
- [ ] Add a multi-stage Dockerfile for the API.
- [ ] Add Compose services for Postgres 16 and the API with health dependency and local-only secrets.
- [ ] Add safe service defaults to `.env.example`.

### Task 5: README and Verification

**Files:**
- Create: `services/README.md`

- [ ] Document `docker compose up --build`, `go test ./...`, `go vet ./...`, `golangci-lint run`, and `sqlc generate`.
- [ ] Document install commands for `sqlc`, `golang-migrate`, and `golangci-lint`.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `sqlc generate` if the binary is available; otherwise report the missing tool with the documented install path.
- [ ] Run a Docker Compose smoke check when Docker is available: start services and verify `/healthz` returns `200`.
