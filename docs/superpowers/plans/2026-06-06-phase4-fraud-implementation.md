# Phase 4 Fraud Implementation Plan

> **Status: complete.** Phase 4 fraud agent shipped; run the acceptance suite with `make test-phase4-e2e`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Phase 4 asynchronous fraud scoring, saga review, privacy, audit, and observability contracts defined under `services/docs/phase4/specs/`.

**Architecture:** Keep fraud scoring transport-independent behind Python ports, with Kafka, gRPC, SQL, metrics, and tracing isolated in integrations. Extend the Go saga and Ledger services through their existing event, repository, outbox, and gRPC boundaries. Preserve asynchronous payment execution and fail-open fraud behavior.

**Tech Stack:** Python 3.11, `uv`, pytest, FastAPI/LLMPort, Go 1.26, gRPC/protobuf, franz-go, pgx, Docker Compose, Prometheus, Grafana, OpenTelemetry.

---

### Task 1: Stable Fraud Contracts

**Files:**
- Create: `services/internal/event/fraud.go`
- Modify: `services/proto/ledger/v1/ledger.proto`
- Test: `services/internal/event/fraud_test.go`

- [x] Write contract tests for schema versions, actions, reason codes, required fields, and stable event IDs.
- [x] Run focused tests and confirm they fail because contracts do not exist.
- [x] Implement bounded Go fraud event contracts and Ledger enrichment RPC messages.
- [x] Generate clients and run contract tests.

### Task 2: Python Fraud Domain and LLM Boundary

**Files:**
- Create: `app/fraud/config.py`, `app/fraud/dto.py`, `app/fraud/ports.py`, `app/fraud/instruction.py`, `app/fraud/guards.py`, `app/fraud/validator.py`, `app/fraud/completion.py`
- Test: `tests/fraud/test_config.py`, `tests/fraud/test_guards.py`, `tests/fraud/test_validator.py`, `tests/fraud/test_completion.py`

- [x] Write focused settings, guard, validator, and completion tests.
- [x] Run tests and confirm missing-domain failures.
- [x] Implement immutable domain types, validated settings, exact rejection codes, canonical action derivation, and a one-call completion adapter.
- [x] Run focused and full Python tests.

### Task 3: Fraud Workflow and Durable Worker Core

**Files:**
- Create: `app/fraud/service.py`, `app/fraud/worker.py`, `app/fraud/repo/store.py`, `app/fraud/repo/migrations/000001_fraud_sessions.sql`
- Test: `tests/fraud/test_service.py`, `tests/fraud/test_worker.py`

- [x] Write fake-port workflow tests for allow, flag, block, guard rejection, enrichment failure, retry success, retry failure, and duplicates.
- [x] Implement deterministic service flow, durable outcome-before-publication worker behavior, and lease/idempotency store contracts.
- [x] Verify prompts contain no source user or wallet IDs and model calls are bounded to two.

### Task 4: Go Saga Fraud Review

**Files:**
- Modify: `services/internal/saga/types.go`, `services/internal/saga/orchestrator.go`, `services/internal/sagaconsumer/consumer.go`, `services/internal/notification/*`
- Create: `services/db/migrations/000009_phase4_fraud_review.up.sql`, `services/db/migrations/000009_phase4_fraud_review.down.sql`
- Test: `services/internal/saga/orchestrator_test.go`, `services/internal/sagaconsumer/consumer_test.go`, `services/internal/notification/*_test.go`

- [x] Write tests proving one scoring request is emitted with payment execution, valid fraud signals only transition `PAYMENT_PROCESSING`, duplicates emit one pause, and review-state payment results are deferred.
- [x] Implement scoring request publication, fraud metadata, `FRAUD_REVIEW`, `tx.paused`, and deferred result behavior.
- [x] Run focused and full Go tests.

### Task 5: Enrichment, Runtime, and Observability

**Files:**
- Modify/Create: Ledger repository and gRPC handlers, `app/fraud/integrations/*`, Compose/Helm/Prometheus/Grafana/Jaeger configuration, `.env.example`
- Test: Go/Python boundary tests and runtime contract tests

- [x] Add sanitized Ledger enrichment RPC implementation and thin Python mapping client with explicit retry classification.
- [x] Add bounded metrics, W3C propagation helpers, worker health/readiness, and pinned local runtime services.
- [x] Add provisioned Prometheus data source and three Grafana dashboards.
- [x] Run contract tests proving no generated imports escape the integration module and no identifier metric labels exist.

### Task 6: Phase 4 Verification

**Files:**
- Create/Modify: Phase 4 end-to-end tests and local test documentation

- [x] Run `uv run pytest`.
- [x] Run `go test ./...` and `go vet ./...` from `services/`.
- [x] Run Compose/Helm contract tests and available Phase 4 end-to-end scenarios.
- [x] Audit every acceptance criterion in specs `00` through `09` against current evidence.
- [x] Push `feat/phase4-fraud` to origin and remove the local worktree after verification.
