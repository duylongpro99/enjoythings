# Phase 3.5: Payment Processor

**Priority:** P5 - external payment execution slice  
**Session size:** One implementation session  
**Depends on:** P1, P2 event expectations

## Goal

Add an idempotent Payment Processor that consumes payment commands, calls a stub payment rail, and publishes payment completion or failure events.

## Problem

The saga cannot complete until an external payment step succeeds or fails. Phase 3 needs this behavior without a real payment provider.

## Scope

- Add `cmd/payment-processor`.
- Consume `payment.execute`.
- Persist payment attempts and status by `payment_id`.
- Call an HTTP stub rail.
- Retry retryable rail failures with exponential backoff and jitter.
- Publish `payment.completed` or `payment.failed`.
- Commit Kafka offsets only after local state and result event handling are safe.

## Out of Scope

- Real payment provider integration.
- Card, bank, or payout credential storage.
- PCI or compliance scope.
- Human payment review.

## Status Model

```text
PENDING -> COMPLETED
PENDING -> FAILED
```

The processor must handle duplicate `payment.execute` commands:

- If `COMPLETED`, republish `payment.completed` and ack.
- If `FAILED`, republish `payment.failed` and ack.
- If `PENDING`, continue or safely retry the rail call according to attempt metadata.

## Retry Policy

Retry only network errors, timeouts, and HTTP `5xx`. Treat HTTP `4xx` as terminal failure. The default backoff is:

```text
1s, 3s, 9s
```

Add jitter to avoid synchronized retries.

## Stub Rail

The stub rail supports deterministic testing:

- success response
- retryable failure followed by success
- terminal failure
- timeout

## Acceptance Criteria

- A duplicate `payment.execute` never charges twice.
- Retryable failures retry up to the configured limit.
- Terminal failures publish `payment.failed`.
- Success publishes `payment.completed`.
- Payment status persists across processor restart.
