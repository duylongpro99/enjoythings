# Phase 2 Spec Index

Phase 2 splits the Phase 1 single Go API into gateway, wallet, and ledger services. These specs are ordered so each implementation session can finish with a compiling, testable repo and a clear next dependency.

## Priority Order

| Priority | Spec | Session goal | Depends on |
|---:|---|---|---|
| P0 | `00-contracts-and-layout.md` | Define protobuf contracts, generation, shared event schema, and service layout | Phase 1 green |
| P1 | `01-wallet-grpc-service.md` | Extract wallet behavior behind a gRPC service | P0 |
| P2 | `02-gateway-rest-to-grpc.md` | Move client HTTP, JWT, rate limiting, and wallet routing into gateway | P1 |
| P3 | `03-ledger-grpc-service.md` | Extract ledger read behavior and append-only storage behind gRPC | P0, P2 preferred |
| P4 | `04-wallet-outbox-and-kafka-publisher.md` | Persist transfer events atomically and publish `tx.initiated` via Kafka | P1 |
| P5 | `05-ledger-kafka-consumer.md` | Consume `tx.initiated` idempotently and append debit/credit ledger entries | P3, P4 |
| P6 | `06-compose-and-e2e-phase2.md` | Wire local Compose and prove gateway to wallet to Kafka to ledger flow | P2, P5 |

## Strategy

Build contracts first, then split synchronous service boundaries, then add asynchronous durability. This avoids coupling service extraction to Kafka behavior and keeps each session small enough to complete, test, and review independently.

## Source Documents

- `services/docs/prd.md`
- `services/docs/architecture.md`
- `services/docs/phase2/prd.md`
- `services/docs/phase2/architecture.md`

