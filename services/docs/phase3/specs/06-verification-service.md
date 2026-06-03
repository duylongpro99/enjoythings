# Phase 3.6: Verification Service

**Priority:** P6 - internal eligibility gate
**Session size:** One implementation session  
**Depends on:** P1

## Goal

Add an internal Verification Service that provides a simple eligibility gate for saga payments.

## Problem

Phase 3 needs a precondition service to exercise distributed validation, state machines, Kafka events, and notifications, but legal identity verification is outside the intended scope.

## Scope

- Add `cmd/verification`.
- Add verification state storage by `user_id`.
- Add `SubmitVerification` and `GetStatus` gRPC APIs.
- Add gateway routes for submit and status.
- Default to `VERIFICATION_MODE=auto`.
- Publish `user.verified` or `user.rejected` events.
- Let Saga Orchestrator check verification status before wallet debit.

## Out of Scope

- Real legal identity verification.
- Document upload.
- Provider webhooks.
- PII-heavy storage.
- Compliance guarantees.

## Modes

```text
VERIFICATION_MODE=auto
VERIFICATION_MODE=manual
VERIFICATION_MODE=rules
```

`auto` is the default. Submitting verification immediately creates or updates the record to `verified` and publishes `user.verified`.

`manual` sets the status to `pending` and requires an internal admin endpoint or tool to approve or reject.

`rules` uses deterministic test inputs to approve, reject, or leave pending.

## State Model

```text
unverified -> pending -> verified
                     -> rejected
unverified -> verified   # auto mode
```

## Acceptance Criteria

- Default local behavior auto-verifies a submitted user.
- Saga rejects unverified users with `FAILED_PRECONDITION`.
- Duplicate submit requests are idempotent.
- `user.verified` is published when status becomes verified.
- No legal identity provider or sensitive document storage is introduced.
