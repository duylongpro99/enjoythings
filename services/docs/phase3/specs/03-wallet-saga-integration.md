# Phase 3.3: Wallet Saga Integration

**Priority:** P3 - required for saga execution  
**Session size:** One implementation session  
**Depends on:** P1, P2 contract expectations

## Goal

Expose idempotent wallet commands that let the Saga Orchestrator debit funds and compensate debit failures without letting Wallet own the full distributed transaction.

## Problem

Current Wallet transfer behavior debits the source wallet, credits the destination wallet, creates a transfer row, and publishes `tx.initiated`. That is correct for Phase 2 but conflicts with Phase 3 orchestration.

## Scope

- Add `DebitForSaga` gRPC command.
- Add `CompensateDebit` gRPC command.
- Persist saga wallet operation records for idempotency.
- Ensure debit is atomic with its idempotency record.
- Ensure compensation is atomic and cannot over-credit.
- Preserve existing Phase 2 wallet APIs unless the migration spec explicitly removes them.

## Out of Scope

- Ledger writes.
- Payment processor calls.
- Kafka publishing for saga compensation.
- Real verification provider calls.

## Command Behavior

`DebitForSaga`:

- Validates `payment_id`, `from_wallet_id`, `amount_cents`, `currency`, and `idempotency_key`.
- Checks wallet ownership if `user_id` is part of the command.
- Locks the source wallet row.
- Verifies sufficient funds.
- Debits balance.
- Records the operation as completed.
- Returns the new balance.

`CompensateDebit`:

- Validates the original `payment_id`.
- Succeeds idempotently if compensation already happened.
- Fails with `FAILED_PRECONDITION` if no matching debit exists.
- Credits back exactly the debited amount.
- Records compensation completion.

## Data Model

Add a wallet operation table or extend transfers with saga-specific operation rows. The table must include:

- `payment_id`
- `operation`
- `idempotency_key`
- `wallet_id`
- `amount_cents`
- `currency`
- `status`
- `created_at`
- `updated_at`

## Acceptance Criteria

- Duplicate debit commands do not debit twice.
- Duplicate compensation commands do not credit twice.
- Insufficient funds returns `FAILED_PRECONDITION`.
- Wallet no longer needs to publish `tx.initiated` for the Phase 3 saga path.
- Existing Phase 2 wallet tests continue to pass unless explicitly migrated.
