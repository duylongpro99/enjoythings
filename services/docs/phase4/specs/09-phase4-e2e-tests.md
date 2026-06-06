# Phase 4.9: End-to-End Tests

**Priority:** P9 - final Phase 4 validation
**Session size:** One implementation session
**Depends on:** P5-P8

## Goal

Prove the complete Phase 4 fraud and observability behavior across retries, malformed model output, duplicate events, review races, audit persistence, and trace propagation.

## Scope

- Add deterministic fake LLM scenarios through an OpenAI-compatible test server or driver fake.
- Add end-to-end fraud and saga scenarios.
- Add audit, tracing, metrics, and dashboard smoke checks.
- Document local test commands.

## Scenarios

| Scenario | Expected result |
|---|---|
| Low-risk payment | Session completes with `allow`; no `fraud.flagged`; saga continues normally |
| High-risk payment | `fraud.flagged` moves active saga to `FRAUD_REVIEW` and emits one `tx.paused` |
| Block verdict | Same Phase 4 saga behavior as `flag`, with action preserved |
| Malformed response then valid response | Validator retries once and stores accepted verdict |
| Two malformed responses | `fraud.error` published; saga continues fail-open |
| Prompt contains raw UUID | Guard rejects before provider call and records bounded rejection reason |
| Model response contains UUID | Validator rejects and retries; no sensitive verdict is published |
| Enrichment unavailable | `fraud.error` published; no flagged event |
| Duplicate scoring request | One logical session and no second model call |
| Duplicate flagged event | At most one review transition and one `tx.paused` |
| Payment result races with fraud review | Deferred result is stored and saga remains `FRAUD_REVIEW` |
| Provider switch | Configuration change selects another fake OpenAI-compatible provider without fraud code changes |
| Audit query | Session includes sanitized enrichment, response, verdict, events, and outcome |
| Distributed trace | One Jaeger trace crosses saga, Kafka, Python graph, gRPC, model, and verdict consumer |
| Metrics and dashboards | Prometheus targets are healthy and fraud dashboard panels return data |
| Audit database unavailable | Saga payment continues, no flagged event publishes, and scoring request remains retryable |
| Unknown payment in flagged event | Consumer records an orphan invariant violation and commits without mutating a saga |
| Conflicting model action | Canonical score-derived action controls publication and mismatch is audited |

## Privacy Assertions

- Captured model requests contain no source user or wallet IDs.
- Logs, spans, and metric labels contain no raw user or wallet IDs.
- Audit rows contain payment ID but no user or wallet IDs.
- Raw model responses appear only in the audit database.

## Test Isolation

- Tests use deterministic IDs, model responses, and clocks.
- Tests create unique Kafka event IDs and clean their dedicated fraud database state.
- External cloud model providers are never required.
- Observability checks use waits capped at 30 seconds and identify the failed boundary.

## Acceptance Criteria

- Tests cover every Phase 4 PRD acceptance criterion after applying the P0 production-trigger decision.
- Retry, duplicate, race, fail-open, and privacy behavior are verified.
- Tests run locally through documented `uv`, Go, and Compose commands.
- A failing test identifies the service or transport boundary that broke.
