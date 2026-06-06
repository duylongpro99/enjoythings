# Phase 4.0: Scope and Decisions

**Priority:** P0 - required before contract changes
**Session size:** One documentation and alignment session
**Depends on:** Phase 3 green

## Goal

Resolve the integration and ownership decisions that otherwise make the Phase 4 PRD incompatible with the implemented Phase 3 saga path.

## Problem

The Phase 4 source documents describe consuming `tx.initiated`, but Phase 3 deliberately removed that event from the production saga path. The source documents also leave identifier privacy, enrichment ownership, audit ownership, and review-state races ambiguous.

## Decisions

### Production Trigger

The Saga Orchestrator publishes `fraud.score.requested` through its existing outbox when it enters `PAYMENT_PROCESSING`. The fraud worker consumes that event.

- The saga also publishes `payment.execute`; fraud scoring does not block payment execution.
- Both outbox records must be inserted before the saga state update commits, so restart recovery cannot enter `PAYMENT_PROCESSING` without a scoring request.
- Legacy `tx.initiated` remains a Phase 2 compatibility event and is not consumed by the Phase 4 production fraud worker.
- Both events use `payment_id` as the partition key and correlation identifier.

### Privacy Boundary

- Kafka and private worker state may carry raw `payment_id`, `user_id`, and wallet IDs because enrichment RPCs require them.
- LLM-facing context replaces identifiers with semantic labels such as `sender`, `recipient`, and `current_payment`.
- Prompts, accepted verdicts, published events, logs, span attributes, and metric labels never contain raw user or wallet IDs.
- Raw model responses are audit-only; sensitive output is rejected and never becomes an accepted verdict.
- The session store may persist `payment_id` for audit correlation. It does not persist raw user or wallet IDs.
- The input guard is the final enforcement point before every model call.

### Enrichment Ownership

- Ledger owns recent transaction summaries and velocity metrics.
- Verification owns KYC status through its existing `GetStatus` RPC.
- Python never queries Go-owned databases directly.
- The Python graph depends only on `FraudDataPort`; generated protobuf imports remain in the gRPC integration module.

### Saga Review Behavior

- `flag` and `block` both move a saga from `PAYMENT_PROCESSING` to `FRAUD_REVIEW`.
- A flagged event received in any other state is recorded and acknowledged without changing state.
- Payment completion or failure received while in `FRAUD_REVIEW` is recorded and acknowledged without automatic state transition.
- Manual resume/reject and replay of deferred payment results are explicitly out of scope for Phase 4.

### Verdict Authority

- The model supplies `risk_score`, `action`, and `reason`, but application code derives the canonical action from the validated score.
- Scores below `FRAUD_SCORE_THRESHOLD` are `allow`.
- Scores from `FRAUD_SCORE_THRESHOLD` up to but excluding `FRAUD_BLOCK_THRESHOLD` are `flag`.
- Scores at or above `FRAUD_BLOCK_THRESHOLD` are `block`.
- Defaults are `FRAUD_SCORE_THRESHOLD=0.75` and `FRAUD_BLOCK_THRESHOLD=0.90`; startup fails if either is outside `[0.0, 1.0]` or the flag threshold is not lower than the block threshold.
- A model-provided action that differs from the canonical action is retained in audit metadata but never controls publication or saga state.

### Failure and Audit Behavior

- Model, validation, enrichment, and audit failures are fail-open and publish `fraud.error` when publication is available.
- Audit persistence is attempted before verdict publication.
- If audit persistence fails, the worker publishes `fraud.error` when possible, logs sanitized metadata, leaves the scoring request uncommitted for retry, and does not publish `fraud.flagged`.
- Leaving a scoring request uncommitted does not block the payment saga because `payment.execute` was already published independently.
- Duplicate scoring requests resolve to one session and one logical verdict by `event_id`.
- Fraud failures never mutate saga state. Only a valid `fraud.flagged` event can move a saga into review.

### Data Ownership

- Fraud sessions use a dedicated TimescaleDB database owned by the Python fraud worker.
- Fraud migrations live separately from Go service migrations.
- Grafana and Prometheus query or scrape through supported service/database interfaces, not application-owned tables outside the fraud audit database.

## Out of Scope

- Synchronous pre-authorization fraud blocking.
- Manual review UI or admin APIs.
- Automatic rejection after 24 hours.
- Legacy `tx.initiated` fraud scoring.
- Real compliance retention policies.

## Acceptance Criteria

- Every Phase 4 spec uses `fraud.score.requested` for the production path.
- Privacy rules distinguish private transport state from LLM-facing and telemetry data.
- Enrichment, audit, and saga-state ownership are explicit.
- Canonical verdict action and threshold validation are explicit.
- Failure behavior is defined for audit, model, enrichment, and publication failures without sacrificing durable audit.
