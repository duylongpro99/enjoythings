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

### REST Request and Response Fields

All REST requests accept `X-Trace-Id`. If the client omits it, the gateway generates one and returns it in every response as `X-Trace-Id`. Mutating REST requests also accept `Idempotency-Key`; the gateway forwards it to gRPC as `idempotency_key`.

`POST /v1/transfers`

- Request fields: `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, optional `payment_id`, optional `description`.
- Required headers: `Authorization`; optional but recommended `Idempotency-Key`, `X-Trace-Id`.
- Success response: HTTP `202 Accepted` with `payment_id`, `status`, `trace_id`.
- Validation and auth responses: HTTP `400` for malformed JSON or invalid scalar fields, `401` for missing or invalid auth, `404` for missing wallets, `422` for unverified user, insufficient funds, or invalid transfer state.

`GET /v1/payments/{payment_id}`

- Request fields: path `payment_id`.
- Success response: HTTP `200` with `payment_id`, `status`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, optional `failure_code`, optional `failure_message`, `created_at`, `updated_at`, `trace_id`.
- Error responses: HTTP `401` for missing or invalid auth, `404` when the payment is unknown.

`POST /v1/verification/submit`

- Request fields: optional `payment_id`, `user_id`, `verification_id`, `decision`, optional `reason`.
- Required headers: internal service auth; optional but recommended `Idempotency-Key`, `X-Trace-Id`.
- Success response: HTTP `200` with `verification_id`, `user_id`, `status`, `decided_at`, `trace_id`.
- Error responses: HTTP `400` for malformed JSON or invalid scalar fields, `401` for missing or invalid internal auth, `404` when the user record is unknown, `422` for invalid verification state transitions.

`GET /v1/verification/status`

- Request fields: query `user_id`.
- Success response: HTTP `200` with `user_id`, `status`, optional `verification_id`, optional `reason`, `updated_at`, `trace_id`.
- Error responses: HTTP `400` for missing `user_id`, `401` for missing or invalid internal auth, `404` when the user verification record is unknown.

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

### Kafka Topic Contracts

All Kafka messages include these headers:

- `trace_id`: same value as `X-Trace-Id` and gRPC metadata `trace_id`.
- `idempotency_key`: command or event idempotency key when available.
- `event_id`: unique event identifier for consumer deduplication.
- `occurred_at`: RFC3339 UTC timestamp.

`payment.execute`

- Producer: Saga Orchestrator.
- Consumer: Payment Processor.
- Consumer group: `payment-processor`.
- Partition key: `payment_id`.
- Payload fields: `event_id`, `payment_id`, `idempotency_key`, `trace_id`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, `ledger_reservation_id`, `wallet_debit_id`, `occurred_at`.

`payment.completed`

- Producer: Payment Processor.
- Consumer: Saga Orchestrator.
- Consumer group: `saga-orchestrator`.
- Partition key: `payment_id`.
- Payload fields: `event_id`, `payment_id`, `idempotency_key`, `trace_id`, `processor_payment_id`, `status`, `completed_at`, `occurred_at`.

`payment.failed`

- Producer: Payment Processor.
- Consumer: Saga Orchestrator.
- Consumer group: `saga-orchestrator`.
- Partition key: `payment_id`.
- Payload fields: `event_id`, `payment_id`, `idempotency_key`, `trace_id`, `failure_code`, `failure_message`, `failed_at`, `occurred_at`.

`tx.completed`

- Producer: Saga Orchestrator.
- Consumer: Notification and Ledger read model if needed.
- Consumer group: `notification-service`; optional read-model group `ledger-read-model`.
- Partition key: `from_wallet_id`.
- Payload fields: `event_id`, `payment_id`, `trace_id`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, `transfer_id`, `completed_at`, `occurred_at`.

`tx.failed`

- Producer: Saga Orchestrator.
- Consumer: Notification.
- Consumer group: `notification-service`.
- Partition key: `from_wallet_id`.
- Payload fields: `event_id`, `payment_id`, `trace_id`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, `failure_code`, `failure_message`, `failed_at`, `occurred_at`.

`user.verified`

- Producer: Verification Service.
- Consumer: Notification.
- Consumer group: `notification-service`.
- Partition key: `user_id`.
- Payload fields: `event_id`, `user_id`, `verification_id`, `trace_id`, `verified_at`, `occurred_at`.

`user.rejected`

- Producer: Verification Service.
- Consumer: Notification.
- Consumer group: `notification-service`.
- Partition key: `user_id`.
- Payload fields: `event_id`, `user_id`, `verification_id`, `trace_id`, `reason`, `rejected_at`, `occurred_at`.

## Idempotency

Idempotency records are owned by the service that accepts the mutating command. Each record stores `idempotency_key`, a canonical request payload hash, response status, response body, `payment_id` when present, and timestamps.

- Duplicate command with same idempotency key and same payload: return the original successful or in-progress response without re-running side effects.
- Conflicting command with same idempotency key and different payload: return gRPC `ALREADY_EXISTS`; the REST gateway maps this to HTTP `409`.
- Saga command keys: `StartPaymentSaga:{idempotency_key}`, `DebitForSaga:{idempotency_key}`, `CompensateDebit:{idempotency_key}`, `ReserveTransfer:{idempotency_key}`, `ConfirmTransfer:{idempotency_key}`, `CancelReservation:{idempotency_key}`, `SubmitVerification:{idempotency_key}`.
- Kafka consumers deduplicate by `event_id` per consumer group. If `event_id` is unavailable, they use `{topic}:{partition}:{offset}` only as a last-resort replay cursor, not as a business idempotency key.
- Event producers use deterministic event types and stable IDs for retried outbox publishes: `{event_type}:{payment_id}` for one-terminal payment events and `{event_type}:{payment_id}:{attempt}` when a business flow can emit multiple attempts.

## Trace Propagation

The trace field is named `trace_id` everywhere except HTTP headers, where it is `X-Trace-Id`.

- HTTP: inbound and outbound requests use `X-Trace-Id`.
- gRPC metadata: gateway and services send `trace_id`; proto request messages also carry `trace_id` for persisted command audit records.
- Kafka headers: producers set `trace_id`; consumers continue it on downstream gRPC, HTTP, event, and log calls.
- Logs: structured logs include `trace_id`, `payment_id` when present, `idempotency_key` when present, and service-specific request IDs.
- A service may generate `trace_id` only at an ingress boundary when no upstream value exists.

## Error Handling

- `INVALID_ARGUMENT`: malformed request.
- `NOT_FOUND`: wallet, payment, or user verification record missing.
- `FAILED_PRECONDITION`: insufficient funds, unverified user, invalid state transition.
- `ALREADY_EXISTS`: conflicting idempotency key for a different command payload.
- `UNAVAILABLE`: dependency temporarily unavailable.
- `INTERNAL`: unexpected persistence or infrastructure error.

The gateway maps `FAILED_PRECONDITION` to HTTP `422`.

### Error Mapping

| Domain error | gRPC status | HTTP status |
|---|---|---|
| Malformed JSON, invalid amount, unsupported currency, missing required IDs | `INVALID_ARGUMENT` | HTTP `400` |
| Missing or invalid auth | `UNAUTHENTICATED` | HTTP `401` |
| Wallet, payment, or user verification record missing | `NOT_FOUND` | HTTP `404` |
| Insufficient funds, unverified user, invalid state transition | `FAILED_PRECONDITION` | HTTP `422` |
| Conflicting idempotency key for a different command payload | `ALREADY_EXISTS` | HTTP `409` |
| Dependency temporarily unavailable | `UNAVAILABLE` | HTTP `503` |
| Unexpected persistence or infrastructure error | `INTERNAL` | HTTP `500` |

## Acceptance Criteria

- All new service contracts have stable request and response fields.
- All Kafka topics have producers, consumers, partition keys, and payload fields.
- Idempotency behavior is specified for duplicate and conflicting commands.
- Trace fields are named consistently across HTTP, gRPC, Kafka, and logs.
