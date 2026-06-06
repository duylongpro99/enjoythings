# PRD — Phase 4: Python AI Fraud Agent + Full Observability

**Phase:** 4 of 4
**Duration:** 3-4 weeks
**Status:** Draft
**Last updated:** 2026-06-06
**Depends on:** Phase 3 complete and green

---

## 1. Goal

Replace the naive "call an LLM API directly" approach with a properly architected Python fraud agent that extends the existing FastAPI/agent runtime and reuses the repository's LLM adapter layer. The agent owns input sanitisation, data enrichment, model invocation, output validation, audit logging, and verdict publication. None of that logic leaks into Go Kafka consumers or saga services.

Additionally, wire up the full observability stack with distributed tracing, Prometheus metrics, and Grafana dashboards.

---

## 2. The problem with Phase 3's original design

The original spec described calling a model API directly from a Kafka consumer. That approach has three critical flaws for a fintech context:

| Flaw | Risk |
|---|---|
| Raw transaction data sent straight to LLM | PII leakage - wallet IDs, amounts, user IDs go into a third-party API with no scrubbing |
| No output validation | LLM hallucinations accepted as fraud verdicts without parsing or schema enforcement |
| Hard-coded model | Switching providers requires rewriting consumer code; no A/B testing, no fallback |

The Python agent harness defined in this phase fixes all three while preserving the existing `app.llm` provider abstraction.

---

## 3. What changes from the original Phase 4 spec

| Area | Original spec | This spec |
|---|---|---|
| Runtime | Google ADK Go service | Python worker in the existing `app` package |
| Agent framework | Go `LlmAgent` | Python LangGraph fraud workflow |
| Model selection | `FRAUD_AGENT_MODEL` and provider-specific Go packages | Existing `LLM_PROVIDERS_JSON` + `LLM_DEFAULT_PROVIDER` adapter registry |
| Provider support | Claude, Gemini, Ollama through ADK | Any provider supported by the existing `LLMPort`, initially OpenAI-compatible endpoints |
| Service integration | Agent tools directly know gRPC/Go service details | Agent calls a Python domain service interface; thin gRPC client layer owns protobuf details |
| Input control | ADK `BeforeModelCallback` | Explicit Python guard node before LLM invocation |
| Output control | ADK `AfterModelCallback` | Explicit Python validation node after LLM invocation |
| Data enrichment | ADK function tools | LangGraph enrichment node calling `FraudDataPort` |
| Audit trail | ADK session service + TimescaleDB | Python fraud session store + TimescaleDB audit rows |
| Observability | ADK session events + OTel | Python and Go OpenTelemetry spans + Prometheus + Grafana |

---

## 4. Features

### 4.1 Fraud detection agent

The fraud agent is a Python workflow in `app/fraud`. It runs as a Kafka worker that consumes `tx.initiated`, enriches the event, scores it with an LLM through the existing adapter layer, validates the verdict, persists an audit record, and publishes fraud events.

The agent is composed of:

**System instruction** - defines the agent's role, risk scoring rubric, output schema, and decision rules. It is versioned in code and never changes at runtime.

**Input guard** - validates the model prompt before provider invocation:
- Reject prompts containing raw UUIDs or sensitive fields.
- Enforce a configurable token/character budget.
- Log the sanitised prompt metadata to the audit store.
- Increment `fraud_callback_rejections_total{callback="before", reason=...}` on rejection.

**Enrichment boundary** - the graph calls a domain interface, not gRPC directly:
- `get_transaction_history(wallet_ref, limit)` returns recent transaction summaries without wallet IDs.
- `get_kyc_status(user_ref)` returns verification state as an enum string.
- `get_velocity_metrics(wallet_ref)` returns aggregate transaction velocity stats.

The concrete implementation is a thin gRPC client layer that calls Go services and maps protobuf messages into Python domain DTOs. The agent graph does not import generated protobuf code and does not know the services are implemented in Go.

**LLM adapter** - the model call uses the existing `app.llm` port/registry:
- Provider config remains in `LLM_PROVIDERS_JSON`.
- Default provider remains `LLM_DEFAULT_PROVIDER`.
- The fraud agent uses a non-streaming completion helper built on the same `LLMPort` contract or a small extension of it.
- Provider-specific code stays in drivers, not in fraud logic.

**Output validator** - validates the model response after provider invocation:
- Parse JSON into `FraudVerdict`.
- Validate required fields and allowed actions.
- Reject malformed output and retry once with a corrective prompt.
- Clamp risk score to `[0.0, 1.0]`.
- If retry also fails, publish `fraud.error` and fail open.

**Session state** - every scoring run carries:
- transfer ID and trace context
- opaque wallet/user references that are safe for prompts and resolvable only by the enrichment boundary
- sanitised transaction facts
- enrichment results
- model provider metadata
- raw LLM response
- parsed verdict
- callback/guard events
- final outcome

The session is persisted to Postgres/TimescaleDB for replay, audit, and dashboard queries.

### 4.2 Model provider abstraction

The existing LLM adapter layer is the only model-provider abstraction. Switching providers must require configuration changes only.

Example provider registry:

```dotenv
LLM_PROVIDERS_JSON='{"providers":[{"id":"local","driver_type":"openai_compatible","base_url":"http://127.0.0.1:11434/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"llama3.1","timeout_seconds":30}]}'
LLM_DEFAULT_PROVIDER=local
LOCAL_LLM_API_KEY=replace-me
```

Provider examples:

| Provider | Config shape | Use case |
|---|---|---|
| OpenAI-compatible cloud model | `driver_type=openai_compatible` with provider base URL | Default flexible integration path |
| Ollama | OpenAI-compatible local endpoint | Offline/local testing |
| LiteLLM proxy | OpenAI-compatible endpoint | Claude, Gemini, custom models, and routing behind one API |

Adding direct Anthropic/Gemini drivers is allowed later, but Phase 4 should first reuse the existing adapter contract.

### 4.3 Service interface for Go integration

Python-to-Go integration must be isolated:

```text
Python fraud agent -> FraudDataPort -> GrpcFraudDataClient -> Go services
```

Rules:

- Agent graph nodes call domain methods only.
- Generated protobuf imports are confined to the gRPC client layer.
- Go services expose narrowly scoped fraud-enrichment RPCs where existing APIs are insufficient.
- Python never queries Go-owned databases directly.
- Tests replace `FraudDataPort` with fakes.

This keeps the polyglot boundary small, testable, and easy to migrate if the fraud agent is later consolidated.

### 4.4 Saga integration with fraud signals

- Fraud agent publishes `fraud.flagged` when verdict action is `flag` or `block`.
- Saga Orchestrator subscribes to `fraud.flagged`.
- If a `fraud.flagged` event arrives while a saga is in `PAYMENT_PROCESSING`, the saga moves to `FRAUD_REVIEW`.
- Emit `tx.paused` so Notification service can alert the user.
- Manual admin resume or auto-reject after 24h can be stubbed in Phase 4.
- If the saga has already reached a terminal state, the fraud event is recorded but does not mutate the saga.

### 4.5 Distributed tracing

- Python FastAPI and fraud worker use OpenTelemetry instrumentation.
- Go services continue using `go.opentelemetry.io/otel`.
- Trace context propagates through HTTP, gRPC metadata, and Kafka headers using `traceparent`.
- Spans are created for: every handler, gRPC call, Kafka produce/consume, DB query, fraud graph node, enrichment call, model call, and verdict publication.
- Fraud session ID is attached to spans as `fraud.session_id`.
- End-to-end trace: client -> gateway -> saga -> wallet -> Kafka -> Python fraud worker -> gRPC enrichment calls -> LLM adapter -> verdict.

### 4.6 Prometheus metrics

- Every Go service exposes metrics on its existing metrics endpoint.
- The Python fraud worker exposes `/metrics` through the FastAPI app or a worker metrics server.
- Custom fraud metrics are defined in section 6.

### 4.7 Grafana dashboards

- System overview: request rates, error rates, p99 latency per service.
- Saga health: saga duration histogram, step failure rates, compensation rate, fraud review count.
- Fraud agent: transactions scored, flag rate, model latency by provider, score distribution, enrichment call counts, guard rejection rate.

---

## 5. Acceptance Criteria

| Scenario | Expected result |
|---|---|
| High-risk transaction | Python worker consumes `tx.initiated`, enriches via `FraudDataPort`, guard validates prompt, LLM scores through `app.llm`, validator accepts verdict, `fraud.flagged` is published |
| LLM returns malformed JSON | Validator rejects, retries once; if retry also fails, `fraud.error` is published and saga continues fail-open |
| Switch model provider | Change `LLM_DEFAULT_PROVIDER` or provider JSON, restart Python service; zero fraud-agent code changes |
| Raw wallet UUID appears in LLM prompt | Input guard rejects the prompt, records callback event, increments `fraud_callback_rejections_total`, and blocks provider call |
| Service integration isolation | Agent graph imports no generated protobuf modules; only the thin gRPC client layer knows protobuf/Go service details |
| Trace in Jaeger | Single payment trace shows Go service spans, Kafka spans, Python fraud graph spans, gRPC enrichment spans, and LLM adapter span |
| TimescaleDB audit | Every agent run produces a `fraud_sessions` row with enrichment log, raw LLM response, parsed verdict, and callback events |
| Grafana fraud dashboard | Fraud metrics render transactions scored, score distribution, model latency, and callback rejection counts |

---

## 6. Custom metrics - fraud agent

| Metric | Type | Labels |
|---|---|---|
| `fraud_transactions_scored_total` | Counter | `action`, `provider` |
| `fraud_model_latency_seconds` | Histogram | `provider`, `model` |
| `fraud_risk_score` | Histogram | buckets: 0.1 steps |
| `fraud_enrichment_calls_total` | Counter | `method`, `outcome` |
| `fraud_callback_rejections_total` | Counter | `callback` (`before`/`after`), `reason` |
| `fraud_session_duration_seconds` | Histogram | `outcome` |
| `fraud_events_published_total` | Counter | `topic`, `outcome` |

---

## 7. Key decisions

| Decision | Choice | Reason |
|---|---|---|
| Agent runtime | Python worker in existing `app` package | Reuses existing FastAPI app, tests, env loading, and LLM adapters |
| Agent workflow | LangGraph | Explicit, testable graph nodes for enrichment, guard, model call, validation, and audit |
| Model abstraction | Existing `app.llm` ports and registry | Avoid duplicate provider routing and keeps model switching config-only |
| Go integration | `FraudDataPort` + thin gRPC client layer | Keeps protobuf/Go details out of agent logic |
| Data access | Go service RPCs, not direct DB reads | Preserves service ownership and avoids cross-service database coupling |
| PII protection | Python input guard with UUID/sensitive-field checks | Prevents raw wallet/user IDs from reaching third-party providers |
| Output validation | Python schema validator with one retry | Rejects malformed verdicts before they affect sagas |
| Session persistence | Python fraud session store backed by Postgres/TimescaleDB | Full audit replay and regulatory traceability |
| Fail mode on model error | Fail open | Availability over risk for this learning project; production can flip to fail closed |
| Score threshold | `FRAUD_SCORE_THRESHOLD`, default `0.75` | Tunable without code redeployment |
