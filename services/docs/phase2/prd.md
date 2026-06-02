# PRD — Phase 2: Split into Services

**Phase:** 2 of 4  
**Duration:** 3–4 weeks  
**Status:** Draft  
**Last updated:** 2026-06-01  
**Depends on:** Phase 1 complete and green

---

## 1. Goal

Extract the monolith into separate Wallet and Ledger services communicating via gRPC. Add an API Gateway for client traffic. Introduce Kafka so that transaction events flow asynchronously between services.

---

## 2. What changes from Phase 1

| Area | Phase 1 | Phase 2 |
|---|---|---|
| Deployment | Single binary | Three binaries (gateway, wallet, ledger) |
| Service calls | In-process function calls | gRPC over the network |
| Events | None | Kafka `tx.initiated` topic |
| Ledger writes | Synchronous SQL | Event-driven, consumes from Kafka |
| Auth | JWT in app middleware | Moved to gateway; downstream services trust gateway |

---

## 3. Features

### 3.1 API Gateway service
- Accepts all client REST/JSON requests
- Validates JWT (moved from individual services)
- Routes to Wallet or Ledger service via gRPC
- Enforces per-user rate limiting (token bucket, in-memory for Phase 2)

### 3.2 Wallet service (gRPC server)
- Implements `WalletService` protobuf contract
- Owns wallet table and balance; enforces invariants
- On `InitiateTransfer`: updates balances in Postgres, publishes `tx.initiated` to Kafka

### 3.3 Ledger service (gRPC server + Kafka consumer)
- Consumes `tx.initiated` from Kafka
- Appends debit and credit entries to the event store table
- Also serves read queries via gRPC: `GetLedgerEntries(wallet_id)`
- Begin event sourcing: ledger table is append-only from this phase forward

### 3.4 Kafka event bus
- Topic: `tx.initiated` (partitioned by `from_wallet_id`)
- Consumer group: `ledger-service`
- At-least-once delivery; ledger consumer must be idempotent (deduplicate by `transfer_id`)

---

## 4. Out of scope for Phase 2

- Saga orchestrator
- KYC, payment processor, notification services
- Redis / read model caching
- Kubernetes
- Distributed tracing (basic structured logging only)

---

## 5. Acceptance Criteria

| Scenario | Expected result |
|---|---|
| Transfer via gateway | Request flows gateway → wallet (gRPC) → Kafka → ledger consumer |
| Ledger query | `GET /v1/ledger/:id` routes through gateway → ledger service (gRPC) → returns entries |
| Kill ledger service, make a transfer, restart ledger | Ledger catches up from Kafka offset — no entries lost |
| Invalid JWT at gateway | Returns 401; wallet service never receives the request |
| Transfer with insufficient funds | Wallet service returns gRPC error; gateway returns 422 |

---

## 6. Key decisions

| Decision | Choice | Reason |
|---|---|---|
| gRPC framework | Standard `google.golang.org/grpc` | No abstraction layer needed at this scale |
| Kafka client | `franz-go` | Pure Go, high performance, better API than Confluent client |
| Proto location | `proto/` at repo root | Shared between gateway, wallet, ledger |
| Partition key | `from_wallet_id` | Events for the same wallet arrive in order |
| Idempotency | Deduplicate by `transfer_id` in ledger consumer | Prevents duplicate ledger entries on Kafka redelivery |
