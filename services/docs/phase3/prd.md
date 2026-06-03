# PRD — Phase 3: Resilience and Distributed Patterns

**Phase:** 3 of 4  
**Duration:** 4–5 weeks  
**Status:** Draft  
**Last updated:** 2026-06-01  
**Depends on:** Phase 2 complete and green

---

## 1. Goal

Add the services and patterns required to handle real-world failure scenarios: a saga orchestrator for distributed transactions, a KYC state machine, a payment processor with idempotency, and deploy the full system to Kubernetes.

---

## 2. What changes from Phase 2

| Area | Phase 2 | Phase 3 |
|---|---|---|
| Transaction coordination | Wallet handles transfer directly | Saga orchestrator drives multi-step flow |
| Payment execution | Not implemented | Payment processor service |
| Compliance | Not implemented | KYC service with state machine |
| User communication | Not implemented | Notification service |
| Deployment | Docker Compose | Kubernetes (minikube / kind) with Helm |

---

## 3. Features

### 3.1 Saga orchestrator
- Receives `StartPaymentSaga` command from gateway
- Drives the payment saga steps in sequence:
  1. Debit wallet (gRPC → Wallet service)
  2. Reserve ledger entry (gRPC → Ledger service)
  3. Execute payment (Kafka command → Payment Processor)
  4. Confirm ledger entry (gRPC → Ledger service)
  5. Publish `tx.completed` (Kafka)
- On failure at any step: execute compensating transactions in reverse order
- Persist saga state to Postgres so that restarts can resume in-progress sagas
- Publish `tx.failed` after compensation completes

### 3.2 Payment processor
- Consumes payment commands from Kafka topic `payment.execute`
- Calls a stub external payment rail (HTTP mock server)
- Idempotency: deduplicate by `payment_id`; never charge twice
- Retry with exponential backoff (max 3 attempts, jitter)
- Publishes `payment.completed` or `payment.failed` to Kafka

### 3.3 KYC service
- State machine per user: `unverified → pending → verified | rejected`
- `POST /v1/kyc/submit` — user submits identity documents (stub acceptance in Phase 3)
- `GET /v1/kyc/status` — returns current state
- Wallet service checks KYC status before allowing transfers (gRPC call)
- Publishes `kyc.verified` / `kyc.rejected` to Kafka
- Notification service listens and sends user confirmation

### 3.4 Notification service
- Kafka consumer: listens to `tx.completed`, `tx.failed`, `kyc.verified`, `kyc.rejected`, `fraud.flagged`
- Dispatches templated messages via a stub email/SMS adapter
- Stateless — no DB

### 3.5 Kubernetes deployment
- Helm chart per service
- Liveness and readiness probes on all services
- `HorizontalPodAutoscaler` on Wallet and Ledger services (CPU-based, target 70%)
- `ConfigMap` for service config
- `Secret` for DB URLs, JWT key, API keys
- Single-node cluster with `kind` for local development

---

## 4. Out of scope for Phase 3

- AI fraud agent (Phase 4)
- Prometheus / Grafana dashboards (Phase 4)
- Jaeger distributed tracing (Phase 4 — but add structured logging with trace IDs now)
- Multi-region or production hardening

---

## 5. Acceptance Criteria

| Scenario | Expected result |
|---|---|
| Happy path payment | All saga steps complete; `tx.completed` published; user notified |
| Payment processor returns failure | Saga compensates; wallet balance restored; `tx.failed` published |
| Saga orchestrator crashes mid-saga | On restart, saga resumes from last completed step |
| Unverified user attempts transfer | Wallet service returns `FAILED_PRECONDITION`; gateway returns 422 |
| User submits KYC and gets verified | State transitions to `verified`; user receives notification |
| `kubectl rollout restart` of wallet service | Zero downtime; readiness probe gates traffic |

---

## 6. Key decisions

| Decision | Choice | Reason |
|---|---|---|
| Saga style | Orchestration (not choreography) | Explicit control flow; easier to trace failures |
| Saga state storage | Postgres | Same DB as wallet — simplest approach for Phase 3 |
| Payment rail | HTTP stub server | Avoid real payment API complexity |
| K8s local tool | `kind` (Kubernetes in Docker) | Lightest local option; no VM required |
| Helm chart structure | One chart per service | Independent versioning and rollback |
