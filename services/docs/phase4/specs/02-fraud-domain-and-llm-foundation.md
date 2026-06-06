# Phase 4.2: Fraud Domain and LLM Foundation

**Priority:** P2 - Python fraud foundation
**Session size:** One implementation session
**Depends on:** P1

## Goal

Create a provider-agnostic, transport-independent Python fraud domain that can be tested without Kafka, gRPC, TimescaleDB, or a real model.

## Scope

- Add `app/fraud/config.py`, `dto.py`, `ports.py`, `instruction.py`, `guards.py`, and `validator.py`.
- Add a non-streaming completion helper over the existing `app.llm.LLMPort`.
- Add required Python dependencies with `uv`.
- Add focused unit tests.

## Domain Types

Define immutable or validated types for:

- `FraudScoreRequest`
- `SanitizedTransactionFacts`
- `TransactionHistoryEntry`
- `VelocityMetrics`
- `KYCStatus`
- `FraudVerdict`
- `FraudSession`
- `FraudOutcome`

`FraudScoreRequest` may contain private raw IDs. `SanitizedTransactionFacts` and all enrichment DTOs must be safe to serialize into a prompt.

## Ports

The fraud domain defines:

- `FraudDataPort` for transaction history, KYC status, and velocity metrics.
- `FraudSessionStore` for idempotent session creation, event append, and completion.
- `FraudPublisher` for flagged and error events.
- `CompletionPort` for a complete provider-agnostic text response.

No domain module imports Kafka, gRPC, protobuf, SQL, OpenTelemetry, or provider SDK packages.

## LLM Integration

Add `CompletionService` over `LLMPort.stream_chat`:

- Accept provider-agnostic `ChatMessage` values.
- Join content deltas into one response.
- Preserve existing driver selection through `LLM_PROVIDERS_JSON` and `LLM_DEFAULT_PROVIDER`.
- Perform one provider invocation per graph model-call node. Provider-driver timeout handling remains behind `LLMPort`; the fraud completion helper adds no independent retry loop.
- Do not add a second provider registry.

## Input Guard

Before every model call:

- Reject UUIDs and configured sensitive keys.
- Reject prompts above `FRAUD_PROMPT_MAX_CHARS`.
- Reject empty prompts.
- Return exactly one of `uuid_detected`, `sensitive_key_detected`, `prompt_too_large`, or `prompt_empty`.

The guard checks the final serialized prompt, not only source DTOs.

## Output Validator

- Parse a JSON object with `risk_score`, `action`, and `reason`.
- Allow actions `allow`, `flag`, and `block`.
- Reject raw responses above `FRAUD_RESPONSE_MAX_CHARS` before JSON parsing.
- Require a concise non-empty reason of at most 300 characters.
- Reject verdict fields containing UUIDs or configured sensitive keys.
- Clamp finite numeric scores in `[-0.01, 1.01]` into `[0.0, 1.0]`; reject values outside that tolerance, non-numeric values, NaN, and infinity.
- Derive canonical action from the configured flag and block thresholds after score validation.
- Preserve the model-provided action separately for audit when it differs from the canonical action.
- Return a corrective prompt for one validation retry.

Validator rejection reasons are exactly `response_too_large`, `invalid_json`, `invalid_schema`, `invalid_action`, `invalid_score`, and `sensitive_output`. These values are safe for audit events and the `callback="after"` metrics label.

## Configuration

Add validated settings for threshold, prompt size, Kafka group/topic names, fraud database URL, gRPC addresses, and worker metrics port.

| Setting | Default | Validation |
|---|---:|---|
| `FRAUD_SCORE_THRESHOLD` | `0.75` | `[0.0, 1.0)` and lower than block threshold |
| `FRAUD_BLOCK_THRESHOLD` | `0.90` | `(0.0, 1.0]` and higher than flag threshold |
| `FRAUD_PROMPT_MAX_CHARS` | `8000` | positive integer |
| `FRAUD_RESPONSE_MAX_CHARS` | `4000` | positive integer |
| `FRAUD_HISTORY_LIMIT` | `20` | integer from `1` through `100` |
| `FRAUD_CONSUMER_GROUP` | `fraud-agent` | non-empty |
| `FRAUD_REQUEST_TOPIC` | `fraud.score.requested` | non-empty |
| `FRAUD_METRICS_PORT` | `9101` | valid TCP port |

Database URLs, provider keys, and other secrets remain environment-only. Invalid settings fail at process startup.

## Acceptance Criteria

- Unit tests use fake ports and no infrastructure.
- Switching provider configuration requires no fraud-domain code change.
- Guard tests prove UUID and sensitive-key rejection before provider invocation.
- Validator tests cover valid JSON, oversized response, malformed JSON, invalid action, score clamping tolerance, invalid score, sensitive output, canonical action derivation, and corrective retry text.
