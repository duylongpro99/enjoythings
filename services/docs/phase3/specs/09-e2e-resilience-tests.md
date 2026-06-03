# Phase 3.9: E2E Resilience Tests

**Priority:** P9 - final Phase 3 validation  
**Session size:** One implementation session  
**Depends on:** P2, P3, P4, P5, P6, P7, P8

## Goal

Prove the Phase 3 system behavior through end-to-end and resilience tests.

## Problem

Distributed features can compile while still failing under retries, restarts, duplicate messages, or partial failure. Phase 3 needs explicit acceptance tests that exercise those cases.

## Scope

- Add local end-to-end smoke tests for the saga path.
- Add duplicate command and duplicate event tests.
- Add orchestrator restart recovery tests.
- Add payment processor retry and failure tests.
- Add verification auto-approval tests.
- Add notification consumption tests.
- Add Kubernetes rollout validation for Wallet.

## Out of Scope

- Load testing.
- Chaos engineering.
- Production observability dashboards.
- Real payment or verification providers.

## Scenarios

| Scenario | Expected result |
|---|---|
| Happy path payment | Saga completes, wallet debited, ledger confirmed, `tx.completed` published, notification dispatched |
| Payment processor terminal failure | Saga cancels ledger reservation, compensates wallet debit, publishes `tx.failed` |
| Orchestrator crash after wallet debit | Restart resumes and does not debit twice |
| Duplicate `payment.execute` | Payment Processor does not charge twice |
| Duplicate `payment.completed` | Saga confirmation remains idempotent |
| Unverified user | Saga returns `FAILED_PRECONDITION`; gateway returns `422` |
| Verification auto mode | Submit immediately sets status to `verified` and publishes `user.verified` |
| Wallet rollout restart | Readiness gates traffic and requests continue succeeding |

## Acceptance Criteria

- E2E tests cover all Phase 3 PRD acceptance criteria.
- Retry and duplicate behavior is tested at service boundaries.
- Tests can run locally with documented commands.
- Failing tests identify the specific service boundary that broke.
