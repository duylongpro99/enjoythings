# Phase 3.0: Scope and Migration

**Priority:** P0 - required first  
**Session size:** One implementation session  
**Depends on:** Phase 2 tests passing

## Goal

Define the Phase 3 migration from Phase 2 direct wallet transfers to saga-orchestrated payments without breaking the existing service boundaries accidentally.

## Problem

Phase 2 currently lets the gateway call the Wallet service directly for `POST /v1/transfers`. Wallet owns balance mutation, transfer creation, and the `tx.initiated` outbox event. Phase 3 moves coordination to a Saga Orchestrator, so the system needs one explicit migration path before service contracts are added.

## Decisions

- Phase 3 payment flow is saga-first.
- Gateway routes new payment requests to `SagaService.StartPaymentSaga`.
- Wallet no longer coordinates multi-step payment completion for the saga path.
- Wallet remains the owner of wallet balances and exposes idempotent debit and compensation commands.
- Ledger remains the owner of ledger entries and exposes reserve, confirm, and cancel commands.
- The Phase 2 `tx.initiated` path may remain for compatibility tests, but it is not the Phase 3 payment path.
- Real legal identity verification is out of scope. Phase 3 uses an internal Verification Service as an eligibility gate.
- `VERIFICATION_MODE=auto` is the default, so submitted users become verified immediately.

## Scope

- Document the Phase 3 service list: gateway, saga-orchestrator, wallet, ledger, payment-processor, verification, notification.
- Decide the canonical external API for starting Phase 3 payments.
- Decide whether legacy `POST /v1/transfers` is replaced or versioned.
- Define compatibility expectations for existing Phase 2 tests.
- Establish naming: Verification Service and `user.verified` / `user.rejected`.

## Out of Scope

- Implementing new services.
- Writing protobuf files.
- Adding Kubernetes manifests.
- Removing Phase 2 code paths.

## Migration Model

The preferred migration is to keep the external HTTP route stable but change its backend dependency:

```text
POST /v1/transfers
  Phase 2: Gateway -> WalletService.InitiateTransfer
  Phase 3: Gateway -> SagaService.StartPaymentSaga
```

The response remains client-friendly and returns a payment or transfer identifier plus a status. The internal ID may be named `payment_id` in saga contracts while the HTTP response can preserve `id` for compatibility.

## Phase 3 Service List

Phase 3 consists of these independently deployable services:

| Service | Phase 3 responsibility |
|---|---|
| Gateway | Keeps the client-facing `POST /v1/transfers` route stable and forwards new transfer/payment requests to `SagaService.StartPaymentSaga`. |
| Saga Orchestrator | Owns payment coordination, saga state, retries, and gRPC compensation commands. |
| Wallet | Owns wallet balances and exposes idempotent debit and debit-compensation commands for saga steps. |
| Ledger | Owns ledger entries and exposes idempotent reserve, confirm, and cancel commands. |
| Payment Processor | Executes payment commands from the saga path through the stub rail and publishes terminal payment events. |
| Verification | Provides the internal eligibility gate and emits `user.verified` / `user.rejected`. |
| Notification | Consumes payment and verification events and dispatches stub notifications. |

## External API Decision

`POST /v1/transfers` remains the canonical external API for starting Phase 3 payments. It is not versioned for Phase 3. Instead, the gateway changes the downstream dependency from Wallet to Saga Orchestrator after the saga contracts exist.

The Phase 3 HTTP response preserves the compatibility shape:

```json
{
  "id": "payment-or-transfer-id",
  "status": "started"
}
```

Saga gRPC contracts may call the identifier `payment_id`. The gateway may map that field to `id` in HTTP responses so existing clients and compatibility tests do not need an immediate response-schema migration.

## Compatibility Expectations

Existing Phase 2 tests may continue to exercise `WalletService.InitiateTransfer`, direct wallet balance mutation, transfer row creation, and `tx.initiated` outbox publishing. Those tests prove legacy compatibility only.

New Phase 3 tests must prove that `POST /v1/transfers` reaches `SagaService.StartPaymentSaga`, that Wallet and Ledger are called through idempotent saga commands, and that compensation is commanded by the Saga Orchestrator through gRPC. Kafka events are status notifications for the saga path, not compensation commands.

## Acceptance Criteria

- The migration choice is documented before contracts are changed.
- The docs clearly distinguish Phase 2 direct transfer behavior from Phase 3 saga payment behavior.
- Verification replaces legal identity provider language in Phase 3 specs.
- Compensation ownership is clear: the saga orchestrator commands compensation through gRPC.
- No runtime behavior changes are introduced by this spec.
