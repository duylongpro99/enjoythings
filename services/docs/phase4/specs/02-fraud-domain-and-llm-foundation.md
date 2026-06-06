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
- Reuse existing timeout behavior where practical; do not add a second provider registry.

## Input Guard

Before every model call:

- Reject UUIDs and configured sensitive keys.
- Reject prompts above `FRAUD_PROMPT_MAX_CHARS`.
- Reject empty prompts.
- Return a bounded rejection reason suitable for metrics and audit events.

The guard checks the final serialized prompt, not only source DTOs.

## Output Validator

- Parse a JSON object with `risk_score`, `action`, and `reason`.
- Allow actions `allow`, `flag`, and `block`.
- Require a concise non-empty reason with a fixed maximum length.
- Reject verdict fields containing UUIDs or configured sensitive keys.
- Clamp finite numeric scores only when slightly outside `[0.0, 1.0]`; reject non-numeric, NaN, or infinite values.
- Return a corrective prompt for one validation retry.

## Configuration

Add validated settings for threshold, prompt size, Kafka group/topic names, fraud database URL, gRPC addresses, and worker metrics port. Defaults are local-development safe and secrets remain environment-only.

## Acceptance Criteria

- Unit tests use fake ports and no infrastructure.
- Switching provider configuration requires no fraud-domain code change.
- Guard tests prove UUID and sensitive-key rejection before provider invocation.
- Validator tests cover valid JSON, malformed JSON, invalid action, invalid score, sensitive output, and corrective retry text.
