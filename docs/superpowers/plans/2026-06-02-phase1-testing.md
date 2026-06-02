# Phase 1 Testing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Phase 1 testing spec by adding the business code and tests required for wallet, transfer, ledger, repository, auth, readiness, and verification gates.

**Architecture:** Add focused domain types and errors, HTTP handlers that depend on small service interfaces, a wallet service that owns business rules and transaction orchestration, and a Postgres repository that owns SQL and migrations. Integration tests use Postgres 16 through testcontainers, with an external `DATABASE_URL` fallback.

**Tech Stack:** Go 1.26, FastAPI is unrelated to this service slice, pgx/v5, sqlc, testcontainers-go, Docker Compose, golangci-lint.

---

### Task 1: Domain Invariants

**Files:**
- Create: `services/internal/domain/errors.go`
- Create: `services/internal/domain/wallet.go`
- Test: `services/internal/domain/wallet_test.go`

- [ ] Write failing tests for debit invariants, unsupported currency, and positive transfer amounts.
- [ ] Run `go test ./internal/domain` from `services/` and verify the tests fail because the symbols are missing.
- [ ] Add minimal domain types and errors.
- [ ] Run `go test ./internal/domain` and verify it passes.

### Task 2: Database Schema and Repository

**Files:**
- Modify: `services/db/migrations/000001_enable_pgcrypto.up.sql`
- Modify: `services/db/migrations/000001_enable_pgcrypto.down.sql`
- Create: `services/db/query/wallets.sql`
- Create: `services/db/query/transfers.sql`
- Create: `services/db/query/ledger.sql`
- Create: `services/internal/repo/wallets.go`
- Test: `services/internal/repo/db_integration_test.go`

- [ ] Write failing integration tests for create/read wallet, transfer commit, insufficient funds rollback, ledger pagination, and concurrent transfer safety.
- [ ] Run the targeted repo integration test and verify it fails on missing schema/repo behavior.
- [ ] Add schema, SQL queries, and repository methods.
- [ ] Run `sqlc generate`.
- [ ] Run the targeted repo integration test and verify it passes.

### Task 3: Service Rules

**Files:**
- Create: `services/internal/wallet/service.go`
- Test: `services/internal/wallet/service_test.go`

- [ ] Write failing unit tests for create wallet, read own wallet, ownership mismatch, unsupported currency, transfer rules, insufficient funds, and ledger ownership/pagination boundaries.
- [ ] Run `go test ./internal/wallet` and verify the tests fail on missing service behavior.
- [ ] Add service implementation against a small transactional store interface.
- [ ] Run `go test ./internal/wallet` and verify it passes.

### Task 4: HTTP Handlers and Router

**Files:**
- Create: `services/internal/handler/wallet.go`
- Create: `services/internal/handler/transfer.go`
- Create: `services/internal/handler/ledger.go`
- Modify: `services/internal/service/router.go`
- Test: `services/internal/handler/wallet_test.go`
- Test: `services/internal/handler/transfer_test.go`
- Test: `services/internal/handler/ledger_test.go`
- Test: `services/internal/service/router_test.go`

- [ ] Write failing handler tests for request parsing, auth context usage, response mapping, invalid UUIDs, invalid cursor/limit, and business errors.
- [ ] Run targeted handler/router tests and verify they fail on missing routes/handlers.
- [ ] Add handlers and wire router dependencies.
- [ ] Run targeted handler/router tests and verify they pass.

### Task 5: Verification Gate

**Files:**
- Existing service files only.

- [ ] Run `go test ./...` from `services/`.
- [ ] Run `go vet ./...` from `services/`.
- [ ] Run `golangci-lint run` from `services/`.
- [ ] Run `sqlc generate` from `services/` and verify no generated diff remains unexpected.
- [ ] Audit each acceptance criterion in `services/docs/phase1/specs/phase1-testing.md` against current files and command output.
