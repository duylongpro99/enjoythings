# Phase 4.4: Fraud Scoring Workflow

**Priority:** P4 - core fraud behavior
**Session size:** One to two implementation sessions
**Depends on:** P2, P3

## Goal

Build a deterministic, testable LangGraph workflow that enriches a payment, protects the model boundary, validates the response, and returns a fraud outcome.

## Scope

- Add `app/fraud/graph.py` and `service.py`.
- Implement system instruction and prompt construction.
- Add graph-node audit events.
- Add one corrective validation retry.
- Add workflow tests with fake ports.

## Workflow

```text
create_session
  -> build_sanitized_context
  -> enrich_transaction
  -> build_prompt
  -> input_guard
  -> call_llm
  -> validate_verdict
       -> retry_prompt -> input_guard -> call_llm -> validate_verdict
  -> complete_session
```

The graph returns a domain outcome. Kafka publication remains outside the graph so graph tests do not require transport concerns.

## Node Rules

- `create_session` is idempotent by source `event_id`.
- `build_sanitized_context` removes all raw identifiers and uses semantic labels.
- `enrich_transaction` may run independent data-port calls concurrently but preserves deterministic output ordering.
- `build_prompt` emits only sanitized structured facts and the versioned system instruction.
- `input_guard` runs before the initial and corrective model calls.
- `call_llm` records provider/model metadata and latency, but raw model text is audit-only.
- `validate_verdict` rejects malformed or sensitive output and retries exactly once.
- `complete_session` persists the final outcome before returning it.
- Guard and validator rejections record the exact contract-defined `before` or `after` rejection code for later metrics.
- A corrective retry contains the validation reason and original sanitized facts, but never embeds the rejected raw model response.

## Verdict Semantics

- `allow` never publishes a fraud verdict event.
- `flag` and `block` return a flagged outcome for worker publication.
- Canonical action is derived only from the validated score and configured flag/block thresholds.
- A mismatch between model-provided and canonical action is recorded as `action_normalized`; it does not trigger a retry.
- Model and validator failures return a fail-open error outcome.

## Error Handling

- Guard rejection never calls the model and returns fail-open.
- Enrichment failure returns fail-open; partial enrichment is not sent to the model.
- A second validation failure returns fail-open.
- Session-completion persistence failure becomes an audit failure and prevents flagged publication.
- Exceptions are converted into one of the contract-defined `fraud.error` reason codes before leaving the service.

## Testing

- Test every node independently where useful.
- Test complete graph paths for allow, flag, block, guard rejection, enrichment failure, malformed response then success, and two malformed responses.
- Assert no prompt contains IDs from the source request.
- Assert the model is called at most twice.
- Assert canonical action, not model action, determines the returned outcome.

## Acceptance Criteria

- The workflow is fully testable with fake ports.
- Provider switching remains configuration-only.
- Every model call passes through the input guard.
- Fail-open outcomes are explicit and carry bounded reason codes.
