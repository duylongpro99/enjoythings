# PRD — Phase 1: The Monolith

**Phase:** 1 of 4  
**Duration:** 2–3 weeks  
**Status:** Draft  
**Last updated:** 2026-06-01

---

## 1. Goal

Build a single Go binary that handles wallet management and ledger operations, backed by Postgres, with JWT authentication. This is the foundation every subsequent phase builds on.

---

## 2. Why a monolith first

Splitting prematurely is the most common microservice mistake. Starting with a monolith means:
- Business logic is correct before distribution complexity is added
- End-to-end flows are easy to test and debug
- The domain model (wallets, transfers, ledger entries) is fully understood before it's split across services

---

## 3. Features

### 3.1 Wallet management
- `POST /v1/wallets` — create a wallet for a user
- `GET /v1/wallets/:id` — get wallet details
- `GET /v1/wallets/:id/balance` — get current balance
- Balance is stored as integer cents (avoid floating point)
- Enforce non-negative balance on every debit — return `422` if insufficient funds

### 3.2 Transfer
- `POST /v1/transfers` — move funds from one wallet to another
- Both debit and credit happen in a single DB transaction — no partial updates
- Return transfer ID, amount, timestamp, and resulting balances

### 3.3 Ledger
- Every transfer creates two ledger entries: one debit, one credit
- `GET /v1/ledger/:wallet_id` — paginated list of ledger entries for a wallet
- Ledger is append-only — no updates or deletes

### 3.4 Authentication
- All endpoints require a valid JWT in the `Authorization: Bearer <token>` header
- JWT contains `user_id` and `role` claims
- Middleware validates signature and expiry; returns `401` on failure
- For Phase 1, a static signing secret is fine (RS256 comes in Phase 2)

### 3.5 Health endpoints
- `GET /healthz` — always returns 200 if the process is alive
- `GET /readyz` — returns 200 only if Postgres connection is healthy

---

## 4. Out of scope for Phase 1

- Kafka / async events
- gRPC
- Redis / caching
- KYC, payments, notifications
- Kubernetes
- Distributed tracing

---

## 5. Acceptance Criteria

| Scenario | Expected result |
|---|---|
| Create two wallets and transfer between them | Both balances update correctly in one DB transaction |
| Transfer with insufficient funds | Returns 422, balances unchanged |
| Request without JWT | Returns 401 |
| Request with expired JWT | Returns 401 |
| `GET /ledger/:id` after 3 transfers | Returns 3 debit entries and 3 credit entries |
| `docker compose up` | App and Postgres start, `/healthz` returns 200 |

---

## 6. Non-functional requirements

- All handler functions have unit tests (mock the DB layer)
- At least one integration test that runs against a real Postgres container
- `go vet` and `golangci-lint` pass with no errors
- README explains how to run locally

---

## 7. Key decisions

| Decision | Choice | Reason |
|---|---|---|
| Router | `chi` | Lightweight, idiomatic, good middleware support |
| DB driver | `pgx` v5 | Fastest Postgres driver for Go |
| Query generation | `sqlc` | Type-safe SQL without ORM magic |
| Migrations | `golang-migrate` | Simple, file-based, widely used |
| Balance type | `int64` (cents) | Avoids float precision bugs |
| Test framework | `testify` | Standard assertion library |
