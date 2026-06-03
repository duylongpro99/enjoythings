# Phase 3 Spec Index

**Priority:** Index - read before P0

Phase 3 adds resilience and distributed coordination patterns on top of the Phase 2 wallet, ledger, gateway, outbox, and Kafka foundation. These specs are ordered so each implementation session can finish with a compiling, testable repo and a clear next dependency.

The core strategy is contract-first vertical slicing: align migration and contracts first, then add the saga path and supporting services, then deploy and verify the system end to end.

## Priority Order

| Priority | Spec | Session goal | Depends on |
|---:|---|---|---|
| P0 | `00-phase3-scope-and-migration.md` | Decide how Phase 2 direct transfers evolve into Phase 3 saga payments | Phase 2 green |
| P1 | `01-contracts-and-topics.md` | Define gRPC, REST, Kafka, idempotency, trace, and error contracts | P0 |
| P2 | `02-saga-orchestrator.md` | Add durable orchestration for the payment saga | P1 |
| P3 | `03-wallet-saga-integration.md` | Add idempotent wallet debit and compensation commands | P1, P2 interface expectations |
| P4 | `04-ledger-saga-integration.md` | Add ledger reserve, confirm, cancel, and read model behavior | P1, P2 interface expectations |
| P5 | `05-payment-processor.md` | Add idempotent payment command consumer and stub rail integration | P1, P2 event expectations |
| P6 | `06-verification-service.md` | Replace real KYC with auto-approved internal eligibility verification | P1 |
| P7 | `07-notification-service.md` | Consume completion, failure, and verification events through stub adapters | P1, P5, P6 |
| P8 | `08-kubernetes-and-helm.md` | Add local Kubernetes deployment with Helm, probes, config, and secrets | P2-P7 |
| P9 | `09-e2e-resilience-tests.md` | Prove happy path, compensation, restart, duplicate event, and rollout behavior | P2-P8 |

## Source Documents

- `services/docs/phase3/prd.md`
- `services/docs/phase3/architecture.md`
- `services/docs/phase2/prd.md`
- `services/docs/phase2/architecture.md`
- `services/docs/phase2/specs/`

## Phase 3 Decisions Captured Here

- Real KYC provider integration is out of scope for Phase 3.
- Phase 3 uses an internal Verification Service with `VERIFICATION_MODE=auto` by default.
- The saga orchestrator owns payment coordination and compensation.
- Wallet and Ledger expose idempotent operations for the saga; Kafka completion events are not used as commands for compensation.
- Kafka event contracts must be defined before service implementation starts.
