# Phase 4.1: Fraud Contracts and Events

**Priority:** P1 - required before fraud or saga implementation
**Session size:** One implementation session
**Depends on:** P0

## Goal

Define stable Kafka, gRPC, identifier, trace, and idempotency contracts for asynchronous fraud scoring.

## Scope

- Add shared Go event types for fraud topics.
- Add narrow Ledger fraud-enrichment RPCs.
- Reuse Verification `GetStatus`.
- Define Kafka headers, partition keys, consumer groups, and duplicate behavior.
- Extend the repository's Buf generation configuration to generate Python protobuf and gRPC clients alongside Go clients.

## Kafka Topics

| Topic | Producer | Consumer | Partition key |
|---|---|---|---|
| `fraud.score.requested` | Saga Orchestrator | Python fraud worker | `payment_id` |
| `fraud.flagged` | Python fraud worker | Saga Orchestrator, Notification | `payment_id` |
| `fraud.error` | Python fraud worker | Observability/admin handling | `payment_id` |
| `tx.paused` | Saga Orchestrator | Notification | `payment_id` |

All events are JSON. All records carry W3C `traceparent` and optional `tracestate` Kafka headers. Payload `trace_id` remains for compatibility and audit correlation but does not replace W3C propagation.

### `fraud.score.requested`

- Consumer group: `fraud-agent`.
- Payload: `event_id`, `payment_id`, `user_id`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, `occurred_at`, `trace_id`.
- Stable event ID: `fraud.score.requested:{payment_id}`.
- Raw identifiers are transport-private and must not enter the prompt or telemetry.

### `fraud.flagged`

- Consumer group: `saga-orchestrator` for saga handling; Notification uses its own group.
- Payload: `event_id`, `source_event_id`, `payment_id`, `session_id`, `action`, `risk_score`, `reason`, `provider_id`, `model_id`, `occurred_at`, `trace_id`.
- Allowed actions: `flag`, `block`.
- Stable event ID: `fraud.flagged:{source_event_id}`.

### `fraud.error`

- Payload: `event_id`, `source_event_id`, `payment_id`, optional `session_id`, `reason_code`, `occurred_at`, `trace_id`.
- `reason_code` is a bounded enum such as `enrichment_failed`, `prompt_rejected`, `model_failed`, `validation_failed`, `audit_failed`, or `publish_failed`.
- Error payloads never contain raw prompts, responses, or exception strings.

### `tx.paused`

- Payload: `event_id`, `payment_id`, `session_id`, `action`, `risk_score`, `reason`, `paused_at`, `occurred_at`, `trace_id`.
- Stable event ID: `tx.paused:{payment_id}`.

## Ledger gRPC Contracts

Add to `LedgerService`:

```text
GetFraudTransactionHistory(wallet_id, limit, trace_id)
  -> repeated {direction, amount_cents, currency, occurred_at}

GetFraudVelocityMetrics(wallet_id, trace_id)
  -> {transactions_last_hour, amount_last_hour_cents,
      average_amount_30d_cents, distinct_recipients_30d}
```

Responses intentionally omit wallet IDs, transfer IDs, ledger entry IDs, balances, and counterparty IDs.

## Idempotency and Delivery

- Fraud worker deduplicates `fraud.score.requested` by `event_id`.
- A completed duplicate returns the stored outcome without calling enrichment or the LLM again.
- An in-progress duplicate is not processed concurrently.
- Publishers use stable event IDs and may republish the same logical event.
- Consumers commit Kafka offsets only after their durable state or fail-open outcome is recorded.

## Acceptance Criteria

- Fraud topics have stable payloads, producers, consumers, partition keys, and event IDs.
- Ledger enrichment RPC responses contain only sanitized aggregate or summary data.
- Buf generation produces Go and Python clients, and fraud modules outside the integration layer import neither generated Python protobuf nor gRPC modules.
- Contract tests reject unknown fraud actions and unbounded error reasons.
