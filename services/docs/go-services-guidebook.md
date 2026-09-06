# Go Services Guidebook

A beginner-friendly tour of how the `services/` Go code is organized: what
microservices and domain-driven design mean *in this repo*, why the folders
(`cmd`, `internal`, `proto`, `gen`, …) exist, and where to put new code.

> Read this alongside the code. Every claim points at a real file so you can
> open it and see the pattern for yourself.

---

## 1. The big picture

`services/` is **one Go module** (`go.mod`, module path `enjoythings/services`)
that builds **many small server binaries**. Each binary is a *microservice*:

| Service            | Entry point                       | Responsibility                        |
| ------------------ | --------------------------------- | ------------------------------------- |
| gateway            | `cmd/gateway/main.go`             | Public REST front door → gRPC clients |
| wallet             | `cmd/wallet/main.go`              | Wallets, balances, transfers          |
| ledger             | `cmd/ledger/main.go`              | Double-entry ledger / reservations    |
| saga-orchestrator  | `cmd/saga-orchestrator/main.go`   | Coordinates multi-step payments       |
| payment-processor  | `cmd/payment-processor/main.go`   | Talks to payment rails                |
| verification       | `cmd/verification/main.go`        | Fraud / verification checks           |
| notification       | `cmd/notification/main.go`        | Sends notifications                   |

They communicate two ways:

- **gRPC** — synchronous request/response between services (contracts in `proto/`).
- **Kafka** — asynchronous events (via the outbox pattern; `internal/outbox`).

Each service owns its own slice of the database and can be deployed and scaled
independently (see `charts/` and `k8s/`).

**Microservices** = the *outer* shape: many independently deployable servers.
**Domain-driven design (DDD)** = the *inner* shape of each service: business
rules live in one place that knows nothing about HTTP, gRPC, or SQL.

---

## 2. The folders, and why each exists

Two of these conventions are enforced by Go's tooling. The rest are our team's
organization.

### `cmd/` — one folder per runnable binary

A Go program's entry point is `package main` with a `func main()`, and you can
only have **one `main` per folder**. To build 8 servers from one module you need
8 folders, and `cmd/` is the conventional home for them.

A `main.go` here is the **composition root**: it does *no business logic*, it
only wires concrete pieces together. See `cmd/wallet/main.go` — it loads config,
connects to the DB, starts the Kafka outbox publisher, registers the gRPC
handler, and handles graceful shutdown. That's all. Keeping `main` thin means the
real logic stays testable without booting a server.

### `internal/` — private packages (enforced by the compiler)

Go has a hard rule: **code under a folder named `internal/` can only be imported
by code rooted at `internal/`'s parent.** So everything in
`enjoythings/services/internal/...` is importable across our services, but a
*different* module physically cannot import it — the build fails.

Why we want that: it guarantees our service internals stay private and
refactorable. Anything meant to be shared publicly goes *outside* `internal/`
(that's why generated protobuf code lives in `gen/`).

### `proto/` — the API contracts

`proto/<svc>/v1/*.proto` define, in a language-neutral format, the messages and
RPCs each service speaks. This is the single source of truth for service-to-service
APIs. Managed with `buf` (`buf.yaml`).

### `gen/` — generated code (outside `internal/` on purpose)

`buf`/protoc turns the `.proto` files into Go code under `gen/` (e.g. the
`walletv1` package imported in `cmd/wallet/main.go`). It lives outside `internal/`
so *every* service can import the shared message types. **Never hand-edit `gen/`;
regenerate it from `proto/`.**

### `internal/repo/queries/` — generated SQL (sqlc)

Hand-written SQL in `.sql` files is compiled by **sqlc** (`sqlc.yaml`) into
type-safe Go in `internal/repo/queries/*.sql.go`. Also generated — don't edit by
hand.

### The rest

- `charts/`, `k8s/` — Helm charts and kind cluster config for deployment.
- `docs/` — architecture notes, phase specs, and design notes (write one before
  building a feature; see `AGENT.md`).
- `Dockerfile`, `Makefile`, `docker-compose.dev.yml` — build & local dev.

---

## 3. DDD layers inside a service

This is the core idea. Take the **wallet** service and follow its layers. Each
layer is its own package with one job, and **dependencies point inward** —
plumbing depends on the domain, never the reverse.

```
gRPC request
  → internal/walletgrpc/   decode protobuf, validate, map errors   [inbound adapter]
    → internal/wallet/     use-case orchestration                  [application]
      → internal/domain/   business rules (pure, no I/O)           [core]
      → internal/repo/     SQL / Postgres (via a Store interface)  [outbound adapter]
  ← translate domain result/error back to protobuf
```

### Layer 1 — `internal/domain/` (the core, pure)

No HTTP, no gRPC, no SQL. Its own `domain/doc.go` says so: *"holds HTTP- and
database-free business types."* Example from `domain/wallet.go`:

```go
func (wallet *Wallet) Debit(amount int64) error {
    if amount <= 0 { return ErrInvalidAmount }
    if wallet.Balance < amount { return ErrInsufficientFunds }
    wallet.Balance -= amount
    return nil
}
```

That is a pure business rule. It imports only `uuid` and `time`. `domain/errors.go`
holds the shared vocabulary of failures (`ErrInsufficientFunds`, `ErrNotFound`, …).
This layer is the most valuable and most stable — protect it from framework churn.

### Layer 2 — `internal/wallet/` (application / use-cases)

`wallet/service.go` answers "what should happen when a user does X." It
*coordinates*; it doesn't do I/O itself. The key pattern is **dependency
inversion** — it depends on an interface it defines, not on the database:

```go
type Store interface {
    CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error)
    GetWallet(context.Context, uuid.UUID) (domain.Wallet, error)
    // ...
}
type Service struct { store Store }
```

Production wiring passes the real Postgres-backed `repo`; tests pass a fake. That's
why `wallet/service_test.go` runs with no database.

### Layer 3 — `internal/walletgrpc/` (inbound adapter / transport)

`walletgrpc/server.go` translates between the outside world's language (protobuf)
and the domain's language. Every method does three things:

1. Parse & validate the wire request (`parseUUID`, check `amount > 0`).
2. Call the application service (`server.app.CreateWallet(...)`).
3. Map the domain result — or error — back to gRPC. `statusFromDomain` turns
   `ErrInsufficientFunds` → gRPC `FailedPrecondition`, `ErrNotFound` → `NotFound`,
   etc.

It depends on an `App` interface, not the concrete `*wallet.Service`. So transport
knows nothing about wallet logic, and wallet logic knows nothing about gRPC.

### Layer 4 — `internal/repo/` (outbound adapter / persistence)

Implements the `Store` interface using real SQL. `repo/wallets.go` wraps the
sqlc-generated queries and returns `domain.Wallet` types the service expects.

**The rule to remember:** every dependency arrow points toward `domain`. `domain`
imports nobody.

---

## 4. The pattern repeats per service

Most services are the same triple: **domain logic + gRPC adapter**.

| Domain/application pkg      | gRPC adapter pkg        |
| -------------------------- | ----------------------- |
| `internal/wallet`          | `internal/walletgrpc`   |
| `internal/ledger`          | `internal/ledgergrpc`   |
| `internal/verification`    | `internal/verificationgrpc` |
| `internal/saga`            | `internal/sagagrpc`     |
| `internal/paymentprocessor`| (Kafka-driven)          |
| `internal/notification`    | (Kafka-driven)          |

Shared `internal/domain` is the common business vocabulary across services.

### Cross-cutting infrastructure packages

- `config` — environment/config loading (`config.LoadWallet`, …).
- `telemetry` — OpenTelemetry tracing and Prometheus metrics.
- `mtls` — mutual TLS between internal services.
- `outbox` — transactional outbox: write an event to the DB in the same
  transaction as the state change, then a publisher relays it to Kafka reliably.
- `middleware`, `deadletter` — request middleware and dead-letter handling.

### Event flow

- `event` — event payload types.
- `*consumer` (`ledgerconsumer`, `sagaconsumer`, …) — read Kafka topics and drive
  the corresponding service.

### The gateway (public front door)

`internal/gateway/` is the one service that faces users:

- `gateway/handler/` — REST handlers.
- `gateway/client/` — gRPC clients that call the internal services.
- `gateway/middleware/` — e.g. rate limiting.

It accepts REST from the outside and fans out to internal services over gRPC.

---

## 5. How a request flows end-to-end (payment example)

A payment is multi-step and uses the **saga** pattern (a sequence of local steps,
each with a compensating undo):

```
Client
  → gateway (REST)                     internal/gateway/handler
    → saga-orchestrator (gRPC)         internal/saga  drives the steps:
        → wallet.DebitForSaga          reserve/deduct funds
        → ledger.Reserve/Confirm       record double-entry
        → verification / fraud check
      (on failure, run compensations: wallet.CompensateDebit, ledger.Cancel)
  ← result back through the gateway
```

Async side effects (notifications, ledger projections) travel over Kafka via the
outbox, so they survive crashes and retries.

---

## 6. Where do I put new code? (cheat sheet)

- **A new business rule** (validation, an invariant) → `internal/domain/`.
  No imports of gRPC/SQL/HTTP allowed.
- **A new use-case / orchestration** → `internal/<service>/service.go`. Depend on
  a `Store` (or client) interface, not concrete infra.
- **A new gRPC endpoint** → add it to `proto/<svc>/v1/*.proto`, regenerate `gen/`,
  then implement in `internal/<service>grpc/server.go`.
- **A new DB query** → write SQL, run sqlc, use it from `internal/repo/`.
- **A new whole service** → `cmd/<name>/main.go` (thin wiring) + an
  `internal/<name>` domain/app package + an `internal/<name>grpc` adapter.
- **A new public REST route** → `internal/gateway/handler/` + a gRPC client in
  `internal/gateway/client/`.
- **Never** edit `gen/` or `internal/repo/queries/*.sql.go` by hand — regenerate.

---

## 7. Golden rules

1. `cmd/main.go` wires; it never contains business logic.
2. `internal/domain` depends on nothing. All arrows point toward it.
3. Application layers depend on **interfaces**, so real infra is swappable with
   fakes in tests.
4. Adapters (`*grpc`, `repo`, `gateway/handler`) translate between the outside
   world and the domain — and hold no business rules.
5. Contracts live in `proto/`; generated code lives in `gen/` and
   `internal/repo/queries/` — treat both as read-only output.
6. Before building a feature, write a design note in `docs/design-notes/`
   (see `AGENT.md`).
