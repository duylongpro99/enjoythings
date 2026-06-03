# Phase 3.2: Saga Orchestrator

**Priority:** P2 - core Phase 3 service  
**Session size:** One to two implementation sessions  
**Depends on:** P1

## Goal

Add a durable Saga Orchestrator that coordinates payment execution across wallet, ledger, payment processor, verification, and notification events.

## Problem

Phase 2 performs transfers inside the Wallet service transaction and emits `tx.initiated`. Phase 3 needs explicit recovery and compensation across services, which requires a persistent coordinator.

## Scope

- Add `cmd/saga-orchestrator`.
- Add saga persistence in Postgres.
- Implement `StartPaymentSaga` and `GetPaymentSaga`.
- Check user eligibility through Verification Service before debit.
- Debit wallet through gRPC.
- Reserve ledger entry through gRPC.
- Publish `payment.execute` through an outbox-backed Kafka publisher.
- Consume `payment.completed` and `payment.failed`.
- Confirm ledger and publish `tx.completed` on success.
- Cancel ledger reservation, compensate wallet debit, and publish `tx.failed` on failure.
- Resume non-terminal sagas on restart.

## Out of Scope

- Real payment rail implementation.
- Real KYC provider integration.
- Kubernetes deployment.
- Distributed tracing backend.

## Saga States

```text
STARTED
VERIFICATION_CHECKED
WALLET_DEBITED
LEDGER_RESERVED
PAYMENT_PROCESSING
LEDGER_CONFIRMED
COMPLETED
COMPENSATING_LEDGER
COMPENSATING_WALLET
FAILED
```

Terminal states are `COMPLETED` and `FAILED`.

## Persistence

The `sagas` table stores:

- `id`
- `payment_id`
- `idempotency_key`
- `user_id`
- `from_wallet_id`
- `to_wallet_id`
- `amount_cents`
- `currency`
- `state`
- `last_error`
- `created_at`
- `updated_at`

`payment_id` is unique. `idempotency_key` is unique per user and command type.

## Idempotency

Repeated `StartPaymentSaga` calls with the same user and idempotency key return the existing saga if the payload matches. If the payload differs, the service returns `ALREADY_EXISTS`.

Every outbound command uses deterministic idempotency keys derived from `payment_id` and step name.

## Recovery

On startup, the orchestrator queries non-terminal sagas and resumes from the last durable state. Steps must be safe to retry because dependent services provide idempotent commands.

## Acceptance Criteria

- Starting a saga persists state before external side effects.
- Duplicate start requests return the existing saga.
- Restarting the orchestrator resumes in-progress sagas.
- Payment failure performs ledger cancellation, wallet compensation, and publishes `tx.failed`.
- Successful payment confirms ledger and publishes `tx.completed`.
