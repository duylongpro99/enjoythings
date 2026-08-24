# Phase 3 E2E Resilience Tests Implementation Plan

> **Status: complete.** Phase 3 resilience suite shipped; run it with `make test-phase3-e2e`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove every Phase 3 PRD acceptance scenario through repeatable local acceptance, smoke, resilience, and Wallet rollout tests.

**Architecture:** Add a fast in-process acceptance harness that connects real Phase 3 components through event and service-boundary adapters, then add explicit deployed-stack validators for Compose and Kubernetes. Close only the wiring and deterministic-test-input gaps that prevent those tests from exercising the documented Phase 3 path.

**Tech Stack:** Go testing, HTTP/gRPC adapters, Docker Compose, Kafka/Postgres local stack, Bash, kind/Kubernetes/Helm

---

### Task 1: Route Phase 3 Transfers Through Saga

**Files:**
- Create: `services/internal/gateway/client/saga.go`
- Create: `services/internal/gateway/client/saga_test.go`
- Create: `services/internal/gateway/handler/payment.go`
- Create: `services/internal/gateway/handler/payment_test.go`
- Modify: `services/internal/config/config.go`
- Modify: `services/internal/config/config_test.go`
- Modify: `services/cmd/gateway/main.go`

- [x] Write failing gateway client, handler, and config tests proving `POST /v1/transfers` starts Saga, `GET /v1/payments/{payment_id}` reads Saga, trace/idempotency values are forwarded, and `FAILED_PRECONDITION` maps to HTTP 422.
- [x] Run `go test ./internal/gateway/... ./internal/config/...` and verify failures identify the missing Saga boundary.
- [x] Implement the Saga gRPC client, payment handlers, gateway wiring, and `SAGA_GRPC_ADDR`.
- [x] Run `go test ./internal/gateway/... ./internal/config/...` and verify the boundary tests pass.

### Task 2: Add Phase 3 Acceptance Harness

**Files:**
- Create: `services/internal/phase3e2e/resilience_test.go`

- [x] Write failing tests for happy path, terminal payment compensation, restart after wallet debit, duplicate `payment.execute`, duplicate `payment.completed`, unverified user, auto verification, payment retry, and notification consumption.
- [x] Run `go test ./internal/phase3e2e -v` and verify each scenario fails at its named boundary before harness implementation.
- [x] Implement deterministic in-memory stores and boundary adapters inside the test package, using the real Saga, Payment Processor, Verification, Notification, and gateway handler components.
- [x] Run `go test ./internal/phase3e2e -v` and verify every named scenario passes.

### Task 3: Make Local Stack Exercise the Saga

**Files:**
- Modify: `services/docker-compose.yml`
- Modify: `services/internal/paymentprocessor/stub_rail_test.go`
- Modify: `services/internal/paymentprocessor/stub_rail.go`
- Create: `services/devtools/phase3_smoke/main.go`
- Modify: `services/devtools/phase3_contract_test.go`

- [x] Add failing contract and stub-rail tests proving Compose starts Saga and valid UUID payments can deterministically request retry and terminal failure behavior.
- [x] Run `go test ./devtools ./internal/paymentprocessor` and verify the missing wiring/scenarios fail.
- [x] Add Saga Compose wiring, deterministic amount-based local rail scenarios, and a smoke command that drives verification, saga success, compensation, and restart recovery against the local stack.
- [x] Run `go test ./devtools ./internal/paymentprocessor` and verify the tests pass.

### Task 4: Add Wallet Rollout Validation

**Files:**
- Create: `services/devtools/k8s_wallet_rollout.sh`
- Modify: `services/devtools/phase3_contract_test.go`
- Modify: `services/Makefile`
- Modify: `services/docs/phase3/kubernetes-local-guide.md`
- Modify: `services/README.md`

- [x] Add a failing contract test requiring a rollout script that scales Wallet to two replicas, waits for readiness, continuously probes gateway requests, restarts Wallet, and fails if any probe fails.
- [x] Run `go test ./devtools -run TestPhase3 -v` and verify it fails because the rollout validator and commands are missing.
- [x] Implement the rollout validator, Make targets, and documented local commands.
- [x] Run `go test ./devtools -run TestPhase3 -v`, `helm lint charts/enjoythings`, and `helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml`.

### Task 5: Completion Audit

**Files:**
- Modify as required by failures found during audit.

- [x] Run `gofmt` on changed Go files.
- [x] Run `go test ./internal/phase3e2e -v`.
- [x] Run `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Run `helm lint charts/enjoythings`.
- [x] Run `helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml`.
- [x] Run the Compose smoke test and Kubernetes Wallet rollout validator when Docker access is available.
- [x] Audit every scenario and acceptance criterion in `services/docs/phase3/specs/09-e2e-resilience-tests.md` against fresh command output and files.
- [x] Commit, push `feat/phase3-e2e-resilience`, and remove the local worktree.
