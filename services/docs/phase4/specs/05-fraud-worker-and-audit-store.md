# Phase 4.5: Fraud Worker and Audit Store

**Priority:** P5 - runnable fraud service
**Session size:** One to two implementation sessions
**Depends on:** P1, P4

## Goal

Run fraud scoring as an idempotent Kafka worker with durable audit sessions and verdict publication.

## Scope

- Add Kafka consumer and publisher integrations.
- Add the worker entrypoint and dependency wiring.
- Add the TimescaleDB fraud session repository and dedicated migrations.
- Add worker-level failure and duplicate handling.
- Add worker integration tests.

## Consumer Flow

```text
consume fraud.score.requested
  -> validate transport event
  -> create or load session by source_event_id
  -> run FraudScoringService when not complete
  -> publish fraud.flagged for flag/block
  -> publish fraud.error for fail-open outcomes
  -> commit offset after durable outcome
```

Malformed transport events are recorded through sanitized logs and metrics, then committed because retries cannot repair them.

## Audit Schema

The dedicated fraud database stores:

- `session_id`
- `source_event_id` unique
- `payment_id`
- `provider_id` and `model_id`
- sanitized transaction facts and enrichment JSON
- raw LLM response
- parsed verdict
- guard, validation, and workflow events
- final outcome and bounded failure reason
- start and completion timestamps

It does not store raw user or wallet IDs. Raw LLM responses are audit-only and never logged.

Fraud migrations live under `app/fraud/repo/migrations/` and are applied by an explicit fraud migration command before worker startup. The worker checks schema availability at readiness but never applies migrations itself.

## Idempotency

- `source_event_id` is unique.
- A duplicate completed request does not call enrichment or the model.
- If verdict publication status is incomplete, a duplicate republishes the same stable output event.
- Session state tracks output event type and publication status.
- Only one worker claims an incomplete session at a time using a 60-second database lease with an expiry timestamp; a crashed worker's session becomes claimable after expiry.

## Failure Handling

- Audit write failure publishes `fraud.error` when possible, leaves the input uncommitted for retry, and never publishes `fraud.flagged`.
- Verdict publication failure leaves publication incomplete and does not commit the input offset.
- `fraud.error` publication failure is logged with sanitized metadata; the fail-open outcome is still committed after audit is durable.
- A claimed session lease is renewed every 20 seconds while scoring is active and released on completion.
- Graceful shutdown stops polling, allows at most 30 seconds for the active operation, releases its lease when possible, and closes Kafka, gRPC, and database clients.

## Readiness and Health

- Liveness reports whether the worker process and event loop are running.
- Readiness requires Kafka connectivity, fraud database connectivity, required schema availability, and successful construction of the configured LLM provider.
- Enrichment service availability is not a readiness requirement because temporary enrichment failure is handled per scoring request.

## Acceptance Criteria

- Every valid request produces one durable session outcome.
- Duplicate requests do not rescore completed sessions.
- Raw prompts and model responses never appear in logs.
- Flagged output is published only after the audit outcome is durable.
- Audit unavailability does not pause the payment saga and does not lose the scoring request.
- Worker integration tests run with fake model and enrichment ports.
- Readiness fails when required audit schema or provider configuration is invalid.
