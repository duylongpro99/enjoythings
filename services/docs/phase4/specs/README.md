# Phase 4 Spec Index

**Priority:** Index - read before P0

Phase 4 adds asynchronous fraud scoring and full observability to the Phase 3 saga payment path. These specs are ordered so each implementation session ends with a compiling, testable repository and explicit contracts for the next session.

The strategy is contract-first vertical slicing: resolve the Phase 3 integration path, define events and RPCs, build the Python fraud domain and workflow, integrate it with the saga, then add observability and prove the complete behavior.

## Priority Order

| Priority | Spec | Session goal | Depends on |
|---:|---|---|---|
| P0 | `00-phase4-scope-and-decisions.md` | Resolve Phase 3 integration, ownership, privacy, and failure-mode decisions | Phase 3 green |
| P1 | `01-fraud-contracts-and-events.md` | Define fraud Kafka events, enrichment RPCs, identifiers, trace headers, and idempotency | P0 |
| P2 | `02-fraud-domain-and-llm-foundation.md` | Add provider-agnostic fraud DTOs, ports, config, completion helper, guard, and validator | P1 |
| P3 | `03-fraud-enrichment-integration.md` | Add Ledger fraud-signal RPCs and the thin Python gRPC integration layer | P1, P2 ports |
| P4 | `04-fraud-scoring-workflow.md` | Build and test the LangGraph scoring workflow | P2, P3 |
| P5 | `05-fraud-worker-and-audit-store.md` | Consume scoring requests, persist sessions, and publish verdict events | P1, P4 |
| P6 | `06-saga-fraud-review-integration.md` | Publish scoring requests and handle flagged verdicts in the saga | P1, P5 event contracts |
| P7 | `07-distributed-tracing.md` | Propagate W3C trace context through HTTP, gRPC, Kafka, Python, and Go | P3-P6 |
| P8 | `08-prometheus-grafana-and-runtime.md` | Add metrics endpoints, Prometheus, Grafana dashboards, and local runtime wiring | P5-P7 |
| P9 | `09-phase4-e2e-tests.md` | Prove fraud, fail-open, provider switching, audit, trace, and dashboard behavior | P5-P8 |

## Source Documents

- `services/docs/phase4/prd.md`
- `services/docs/phase4/architecture.md`
- `services/docs/phase3/specs/`
- `services/docs/phase3/architecture.md`

## Phase 4 Decisions Captured Here

- The Phase 4 production path consumes saga-owned `fraud.score.requested`, not legacy Phase 2 `tx.initiated`.
- Fraud scoring is asynchronous and fail-open. It never delays publishing `payment.execute`.
- Raw identifiers may exist in private worker state and gRPC requests but never enter LLM prompts, model responses, metrics, or logs.
- Ledger owns transaction-history and velocity enrichment; Verification owns KYC status.
- The existing `app.llm` registry remains the only provider abstraction.
- `flag` and `block` both move an active `PAYMENT_PROCESSING` saga to `FRAUD_REVIEW` in Phase 4.
- Fraud audit data lives in a dedicated TimescaleDB database owned by the Python fraud worker.
