# Phase 4.6: Saga Fraud Review Integration

**Priority:** P6 - saga reaction to fraud signals
**Session size:** One implementation session
**Depends on:** P1, P5 event contracts

## Goal

Connect the Phase 3 Saga Orchestrator to asynchronous fraud scoring without delaying or corrupting payment processing.

## Scope

- Publish `fraud.score.requested` from the saga outbox.
- Add `FRAUD_REVIEW` state and persisted fraud metadata.
- Consume `fraud.flagged`.
- Publish `tx.paused`.
- Record terminal, duplicate, and race outcomes.
- Update Notification handling for `tx.paused`.

## Scoring Request Publication

When advancing from `LEDGER_RESERVED`:

- Enqueue `payment.execute` and `fraud.score.requested` using stable event IDs.
- Use `payment_id` as both partition key and correlation key.
- Enqueue both records and transition to `PAYMENT_PROCESSING` in the same database transaction.
- Retry or restart must not create logically distinct request events.

Fraud scoring remains asynchronous. The Saga Orchestrator does not wait for a verdict.

## Flagged Event Handling

For a valid `fraud.flagged`:

- Load saga by `payment_id`.
- Persist `fraud_session_id`, action, risk score, reason, and flagged timestamp.
- If current state is `PAYMENT_PROCESSING`, transition to `FRAUD_REVIEW` and enqueue `tx.paused`.
- If current state is already `FRAUD_REVIEW` or terminal, acknowledge without another state transition or paused event.
- If current state is any other non-terminal state, record the signal and acknowledge without changing state.
- Unknown `payment_id` is recorded as an orphaned fraud event and committed immediately. A valid fraud event can only originate from a scoring request emitted after the saga record is durable, so retrying would block the Kafka partition without repairing the invariant violation.

`flag` and `block` behave identically in Phase 4.

## Review-State Races

- `payment.completed` or `payment.failed` received while in `FRAUD_REVIEW` is recorded as a deferred terminal result and acknowledged.
- The saga remains `FRAUD_REVIEW`.
- Applying deferred results through manual resume/reject is out of scope, but storing them prevents data loss.
- At most one deferred terminal result is stored. A conflicting second terminal result is recorded as an invariant violation and does not overwrite the first.
- Duplicate payment or fraud events remain idempotent.

## Persistence

Extend saga persistence with nullable fraud metadata, deferred payment-result JSON, and orphan/invariant audit records. Do not overload `last_error` with fraud details.

## Notification

Notification consumes `tx.paused` through its existing dispatcher abstraction. The stub message contains payment ID, action, risk score, and sanitized reason.

## Acceptance Criteria

- Every production saga emits one logical scoring request.
- Fraud signals mutate only `PAYMENT_PROCESSING` sagas.
- Terminal sagas never reopen.
- Payment results arriving during review are durable and acknowledged.
- Duplicate fraud events emit at most one `tx.paused`.
- Conflicting deferred terminal results do not overwrite the first durable result.
