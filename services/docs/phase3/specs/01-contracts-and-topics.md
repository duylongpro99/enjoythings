# Phase 3.1: Contracts and Topics

**Priority:** P1 - required before service implementation  
**Session size:** One implementation session  
**Depends on:** P0

## Goal

Define the Phase 3 REST, gRPC, Kafka, idempotency, error, and trace contracts before implementing services.

## Problem

Phase 3 introduces several new service boundaries. Without a contract-first spec, the saga, wallet, ledger, payment processor, verification, and notification services can make incompatible assumptions about IDs, events, retries, and ownership.

## Scope

- Add protobuf contracts for Saga, Wallet saga commands, Ledger saga commands, Verification, and optional Notification health/control APIs.
- Define REST gateway routes and response codes.
- Define Kafka topics, payload fields, partition keys, and consumer groups.
- Define idempotency keys for commands and events.
- Define trace propagation through HTTP headers, gRPC metadata, Kafka headers, and logs.
- Define error mapping from domain errors to gRPC and HTTP status.

## Out of Scope

- Service implementation.
- Database migrations except schema names referenced by contracts.
- Helm charts.

## REST Contracts

The gateway exposes:

- `POST /v1/transfers` starts a Phase 3 saga payment.
- `GET /v1/payments/{payment_id}` returns saga/payment status if implemented in Phase 3.
- `POST /v1/verification/submit` submits internal verification.
- `GET /v1/verification/status` returns current verification state.

`POST /v1/transfers` returns `202 Accepted` when the saga starts asynchronously. Synchronous validation failures return `400`, `401`, `404`, or `422`.

## gRPC Contracts

Define these service contracts:

- `SagaService.StartPaymentSaga`
- `SagaService.GetPaymentSaga`
- `WalletService.DebitForSaga`
- `WalletService.CompensateDebit`
- `LedgerService.ReserveTransfer`
- `LedgerService.ConfirmTransfer`
- `LedgerService.CancelReservation`
- `VerificationService.SubmitVerification`
- `VerificationService.GetStatus`

All mutating commands include:

- `payment_id`
- `idempotency_key`
- `trace_id`
- request-specific IDs and amounts

## Kafka Topics

| Topic | Producer | Consumer | Partition key |
|---|---|---|---|
| `payment.execute` | Saga Orchestrator | Payment Processor | `payment_id` |
| `payment.completed` | Payment Processor | Saga Orchestrator | `payment_id` |
| `payment.failed` | Payment Processor | Saga Orchestrator | `payment_id` |
| `tx.completed` | Saga Orchestrator | Notification, Ledger read model if needed | `from_wallet_id` |
| `tx.failed` | Saga Orchestrator | Notification | `from_wallet_id` |
| `user.verified` | Verification Service | Notification | `user_id` |
| `user.rejected` | Verification Service | Notification | `user_id` |

Events are JSON in Phase 3 unless a later spec explicitly upgrades all producers and consumers to protobuf together.

## Error Handling

- `INVALID_ARGUMENT`: malformed request.
- `NOT_FOUND`: wallet, payment, or user verification record missing.
- `FAILED_PRECONDITION`: insufficient funds, unverified user, invalid state transition.
- `ALREADY_EXISTS`: conflicting idempotency key for a different command payload.
- `UNAVAILABLE`: dependency temporarily unavailable.
- `INTERNAL`: unexpected persistence or infrastructure error.

The gateway maps `FAILED_PRECONDITION` to HTTP `422`.

## Acceptance Criteria

- All new service contracts have stable request and response fields.
- All Kafka topics have producers, consumers, partition keys, and payload fields.
- Idempotency behavior is specified for duplicate and conflicting commands.
- Trace fields are named consistently across HTTP, gRPC, Kafka, and logs.
