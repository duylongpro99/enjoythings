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
- Real KYC is out of scope. Phase 3 uses an internal Verification Service as an eligibility gate.
- `VERIFICATION_MODE=auto` is the default, so submitted users become verified immediately.

## Scope

- Document the Phase 3 service list: gateway, saga-orchestrator, wallet, ledger, payment-processor, verification, notification.
- Decide the canonical external API for starting Phase 3 payments.
- Decide whether legacy `POST /v1/transfers` is replaced or versioned.
- Define compatibility expectations for existing Phase 2 tests.
- Establish naming: Verification Service and `user.verified` / `user.rejected`, not KYC events.

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

## Acceptance Criteria

- The migration choice is documented before contracts are changed.
- The docs clearly distinguish Phase 2 direct transfer behavior from Phase 3 saga payment behavior.
- Verification replaces real KYC language in Phase 3 specs.
- Compensation ownership is clear: the saga orchestrator commands compensation through gRPC.
- No runtime behavior changes are introduced by this spec.
