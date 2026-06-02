# Architecture — Fintech Microservice Platform

**Version:** 1.0  
**Status:** Draft  
**Last updated:** 2026-06-01

---

## 1. System Overview

The platform is a set of independently deployable Go microservices connected by a Kafka event bus. Synchronous service-to-service calls use gRPC. All services are stateless at runtime; state lives in dedicated data stores owned by each service (polyglot persistence). The system is deployed on Kubernetes.

```
Client
  │
  ▼
API Gateway  (JWT auth, rate limiting, routing)
  │    │    │    │
  ▼    ▼    ▼    ▼
Wallet  Ledger  KYC  Notification
  │       │      │
  └───────┴──────┘
          │
          ▼
     Kafka Event Bus
    ┌─────┴──────────────┐
    ▼                    ▼
Saga Orchestrator    AI Fraud Agent
Payment Processor
```

---

## 2. Services

### 2.1 API Gateway
- **Role:** Single entry point for all client traffic
- **Tech:** Go + `chi` router, JWT middleware, rate limiter
- **Responsibilities:** Authenticate requests, enforce rate limits, route to downstream services via gRPC
- **Exposes:** REST (HTTP/1.1 + JSON) to clients

### 2.2 Wallet Service
- **Role:** Owns wallet balances
- **Tech:** Go, gRPC server, `pgx` for Postgres, Redis for read model
- **Responsibilities:** Create wallets, enforce non-negative balance, publish `tx.initiated` to Kafka
- **Data store:** Postgres (source of truth), Redis (read model cache)

### 2.3 Ledger Service
- **Role:** Double-entry accounting record
- **Tech:** Go, gRPC server, event sourcing
- **Responsibilities:** Append debit/credit events, project read models, never mutate past events
- **Data store:** Event store (append-only Postgres table or EventStoreDB)
- **Pattern:** CQRS — write side appends events, read side projects them into Redis

### 2.4 KYC Service
- **Role:** Identity verification state machine
- **Tech:** Go, gRPC server
- **Responsibilities:** Manage verification states, block unverified users, emit `kyc.verified` / `kyc.rejected`
- **Data store:** Postgres

### 2.5 Payment Processor
- **Role:** Execute outbound payments
- **Tech:** Go, Kafka consumer, idempotency layer
- **Responsibilities:** Process payments with retry + backoff, guarantee exactly-once via idempotency keys, emit `payment.completed` / `payment.failed`
- **Data store:** Postgres (idempotency keys + payment records)

### 2.6 Saga Orchestrator
- **Role:** Coordinate distributed payment transactions
- **Tech:** Go, Kafka consumer + producer, outbox pattern
- **Responsibilities:** Drive saga steps, trigger compensating transactions on failure, persist saga state
- **Pattern:** Orchestration-based saga (not choreography) for explicit failure handling
- **Data store:** Postgres (saga state)

### 2.7 Notification Service
- **Role:** Deliver user-facing notifications
- **Tech:** Go, Kafka consumer
- **Responsibilities:** Listen for domain events, render templates, dispatch via SMS/email/push
- **Data store:** None (stateless dispatcher)

### 2.8 AI Fraud Agent
- **Role:** Real-time transaction risk scoring
- **Tech:** Go, Kafka consumer, Claude API client
- **Responsibilities:** Consume `tx.initiated`, build transaction context, call Claude API, emit `fraud.flagged` for high-risk transactions, persist scores
- **Data store:** TimescaleDB (time-series score history)

---

## 3. Communication Patterns

| Pattern | Used for |
|---|---|
| gRPC (sync) | Client-facing reads, service-to-service queries (wallet balance check, KYC status) |
| Kafka (async) | Domain events: transaction lifecycle, KYC changes, fraud signals |
| Outbox pattern | Guarantee Kafka publish is atomic with DB write — prevents lost events |

---

## 4. Data Stores

| Service | Store | Reason |
|---|---|---|
| Wallet | Postgres | ACID balance updates with row-level locking |
| Wallet read model | Redis | Sub-millisecond balance reads |
| Ledger | Event store (Postgres append-only) | Immutable audit log |
| Ledger read model | Redis | Fast projected balance queries |
| KYC | Postgres | State machine persistence |
| Payment Processor | Postgres | Idempotency key deduplication |
| Saga Orchestrator | Postgres | Saga state and compensation log |
| AI Fraud Agent | TimescaleDB | Time-series scoring history and trend queries |

---

## 5. API Gateway Contract

All client requests arrive as REST JSON. The gateway translates to gRPC for downstream calls.

```
POST /v1/wallets                   → Wallet service: CreateWallet
GET  /v1/wallets/:id/balance       → Wallet service: GetBalance
POST /v1/transfers                 → Wallet service: InitiateTransfer
POST /v1/payments                  → Saga Orchestrator: StartPaymentSaga
GET  /v1/kyc/:user_id/status       → KYC service: GetKYCStatus
POST /v1/kyc/:user_id/submit       → KYC service: SubmitDocuments
```

---

## 6. Kafka Topics

| Topic | Producer | Consumers |
|---|---|---|
| `tx.initiated` | Wallet service | Saga Orchestrator, AI Fraud Agent, Ledger service |
| `tx.completed` | Saga Orchestrator | Ledger service, Notification service |
| `tx.failed` | Saga Orchestrator | Wallet service (compensate), Notification service |
| `kyc.verified` | KYC service | Wallet service (unblock), Notification service |
| `kyc.rejected` | KYC service | Notification service |
| `payment.completed` | Payment Processor | Saga Orchestrator |
| `payment.failed` | Payment Processor | Saga Orchestrator |
| `fraud.flagged` | AI Fraud Agent | Saga Orchestrator (pause/cancel), Notification service |

---

## 7. Saga Flow — Payment

```
1. Client → POST /v1/payments
2. Gateway → Saga Orchestrator: StartSaga
3. Orchestrator → Wallet service: DebitWallet
4. Wallet publishes tx.initiated → Kafka
5. Orchestrator → Ledger service: ReserveLedgerEntry
6. Orchestrator → Payment Processor: ExecutePayment
7. Payment Processor publishes payment.completed → Kafka
8. Orchestrator → Ledger service: ConfirmLedgerEntry
9. Orchestrator publishes tx.completed → Kafka
10. Notification service sends user confirmation

On failure at any step:
- Orchestrator triggers compensating transactions in reverse order
- Wallet balance is restored
- tx.failed is published
```

---

## 8. Observability Stack

| Tool | Purpose |
|---|---|
| Prometheus | Metrics scraping from all services |
| Grafana | Dashboards — request rates, error rates, saga durations, fraud scores |
| Jaeger | Distributed tracing via OpenTelemetry — trace spans across all service hops |
| Structured logs | JSON logs with `trace_id` field for log-to-trace correlation |

Every service exports:
- `http_requests_total` (or `grpc_requests_total`)
- `http_request_duration_seconds`
- `kafka_messages_consumed_total`
- Custom business metrics (e.g. `fraud_flags_total`, `saga_failures_total`)

---

## 9. Kubernetes Deployment

- One `Deployment` per service, minimum 2 replicas
- `HorizontalPodAutoscaler` on Wallet, Ledger, and AI Fraud Agent
- `ConfigMap` for non-secret config, `Secret` for DB credentials and API keys
- Liveness probe: `/healthz` — is the process alive?
- Readiness probe: `/readyz` — is the service ready to accept traffic?
- Helm charts per service under `charts/`

---

## 10. Repository Layout

```
fintech-platform/
├── services/
│   ├── gateway/
│   ├── wallet/
│   ├── ledger/
│   ├── kyc/
│   ├── payment-processor/
│   ├── saga-orchestrator/
│   ├── notification/
│   └── fraud-agent/
├── proto/                  # Shared protobuf definitions
├── pkg/                    # Shared Go packages (auth, kafka, tracing)
├── charts/                 # Helm charts per service
├── docker-compose.yml      # Local dev environment
└── docs/
    ├── PRD.md
    ├── Architecture.md
    └── Roadmap.md
```

---

## 11. Security

- JWT signed with RS256; public key served by API Gateway
- All gRPC calls use mTLS within the cluster
- Secrets injected via Kubernetes Secrets, never in environment files committed to git
- Kafka topics use ACLs — each service has its own service account with produce/consume permissions scoped to its topics only
