# Phase 4.7: Distributed Tracing

**Priority:** P7 - cross-service trace continuity
**Session size:** One to two implementation sessions
**Depends on:** P3-P6

## Goal

Produce one W3C trace across client ingress, Go saga services, Kafka, the Python fraud graph, gRPC enrichment, LLM invocation, and verdict handling.

## Scope

- Configure OpenTelemetry SDKs and OTLP export for Go and Python.
- Instrument HTTP, gRPC, Kafka, database, fraud graph, and LLM boundaries.
- Propagate `traceparent` and optional `tracestate`.
- Add Jaeger to local runtime.
- Add trace propagation tests.

## Propagation Rules

- HTTP uses standard W3C headers.
- gRPC uses `traceparent` and optional `tracestate` metadata.
- Kafka uses record headers with the same names.
- Existing payload `trace_id` remains for compatibility but does not create or continue spans.
- Consumers extract upstream context before creating consume spans.
- Producers inject the current context after creating produce spans.

## Required Spans

Go:

- Gateway HTTP and outbound gRPC calls.
- Saga orchestration steps and outbox enqueue.
- Kafka produce/consume.
- Wallet, Ledger, Verification, Payment Processor, and Notification handlers.
- Database operations at repository boundaries.

Python:

- `fraud.worker.consume`
- each fraud graph node
- each enrichment RPC
- input guard and output validation
- `fraud.llm.complete`
- audit writes
- Kafka publication

## Attribute Policy

Allowed attributes include service name, topic, operation, provider ID, model ID, fraud session ID, payment ID, verdict action, and bounded outcome code.

Never attach raw user IDs, wallet IDs, prompts, model responses, fraud reasons, SQL, Kafka payloads, or credentials.

`payment_id` and `fraud.session_id` are allowed high-cardinality span attributes for trace investigation, but they are forbidden metric labels.

## Error and Sampling Policy

- Record bounded error type and status without raw exception content.
- Local development samples all traces.
- Non-local sampling uses `OTEL_TRACES_SAMPLER=parentbased_traceidratio` and `OTEL_TRACES_SAMPLER_ARG`; the default ratio is `0.1`.
- Trace export failure never changes business behavior.

## Testing

- Unit tests verify Kafka and gRPC injection/extraction.
- Integration test starts with a known `traceparent` and asserts downstream spans share the trace ID.
- Attribute-policy tests or focused assertions prevent sensitive values from entering spans.

## Acceptance Criteria

- Jaeger shows one trace from saga request publication through fraud verdict handling.
- Kafka and gRPC boundaries preserve parent-child relationships.
- Trace export failure is non-fatal.
- Telemetry contains no raw user or wallet identifiers.
