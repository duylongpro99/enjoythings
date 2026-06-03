# Phase 3.7: Notification Service

**Priority:** P7 - user communication and event fanout  
**Session size:** One implementation session  
**Depends on:** P1, P5, P6

## Goal

Add a stateless Notification Service that consumes domain events and dispatches templated messages through stub adapters.

## Problem

Phase 3 acceptance criteria require user notification for successful payments, failed payments, and verification changes. The system needs notification behavior without external email or SMS providers.

## Scope

- Add `cmd/notification`.
- Consume `tx.completed`, `tx.failed`, `user.verified`, and `user.rejected`.
- Render simple templates.
- Dispatch through stub email and SMS adapters.
- Log notification outcomes with trace IDs.
- Commit Kafka offsets after dispatch is handled.

## Out of Scope

- Real email or SMS provider integration.
- Notification preference management.
- Template editor UI.
- Fraud events from Phase 4.
- Notification database.

## Delivery Model

The service is stateless. For Phase 3, delivery means the stub adapter accepts the message and logs it. Failed adapter dispatch should be retryable through Kafka redelivery.

## Idempotency

Because the service has no DB, it should produce deterministic message IDs from event type and aggregate ID, then include them in logs and adapter calls. Exact once delivery is out of scope; idempotent stub adapters are acceptable for local tests.

## Acceptance Criteria

- `tx.completed` produces a payment success notification.
- `tx.failed` produces a payment failure notification.
- `user.verified` produces a verification success notification.
- `user.rejected` produces a verification rejected notification.
- Phase 4 fraud notifications are not required in Phase 3.
