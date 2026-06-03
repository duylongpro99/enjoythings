# Phase 3.4: Ledger Saga Integration

**Priority:** P4 - required for saga accounting  
**Session size:** One implementation session  
**Depends on:** P1, P2 contract expectations

## Goal

Expose ledger reservation, confirmation, cancellation, and read behavior for saga-orchestrated payments.

## Problem

The current Ledger service appends debit and credit entries after consuming `tx.initiated`. Phase 3 requires the saga to reserve ledger effects before external payment execution and confirm or cancel those effects afterward.

## Scope

- Add `ReserveTransfer` gRPC command.
- Add `ConfirmTransfer` gRPC command.
- Add `CancelReservation` gRPC command.
- Persist reservation state idempotently.
- Keep ledger entries append-only for confirmed records.
- Define read behavior for pending, confirmed, and canceled records.

## Out of Scope

- Wallet balance mutation.
- Payment rail execution.
- Notification dispatch.
- Ledger compensation by consuming `tx.failed`.

## State Model

Ledger transfer reservation states:

```text
RESERVED -> CONFIRMED
RESERVED -> CANCELED
```

Confirmed entries are append-only. Canceled reservations do not appear as settled ledger entries unless an audit view is explicitly added.

## Idempotency

Commands are idempotent by `payment_id` and operation:

- Repeated `ReserveTransfer` with the same payload returns the existing reservation.
- Repeated `ConfirmTransfer` returns success if already confirmed.
- Repeated `CancelReservation` returns success if already canceled.
- Conflicting payloads return `ALREADY_EXISTS` or `FAILED_PRECONDITION`.

## Acceptance Criteria

- Reservation can be retried without duplicate ledger records.
- Confirmation appends exactly one debit and one credit ledger entry.
- Cancellation does not append settled entries.
- Confirmation after cancellation fails.
- Cancellation after confirmation fails.
- Existing ledger read APIs remain stable for confirmed entries.
