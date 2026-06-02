# Phase 2.0: Contracts and Layout

**Priority:** P0 - required first  
**Session size:** One implementation session  
**Depends on:** Phase 1 tests passing

## Goal

Define the service contracts, generated-code workflow, shared event schema, and target folders needed to split the Phase 1 API into gateway, wallet, and ledger services.

## Problem

Phase 1 currently runs as one Go API under `services/cmd/api` with internal packages for auth, handlers, repo, and wallet behavior. Phase 2 needs network boundaries, but implementing service binaries before contracts would force every later session to rediscover message shapes and package ownership.

## Structure

Add protobuf files under `services/proto/wallet/v1` and `services/proto/ledger/v1`, plus a generation path that places Go outputs inside the `services` module. Add a small shared event package for the Phase 2 JSON Kafka event, because wallet publishing and ledger consuming must agree on field names before Kafka exists in the code. Create only minimal service directories needed for later work; do not move business logic yet.

## Scope

- Add `WalletService` protobuf contract with `CreateWallet`, `GetBalance`, and `InitiateTransfer`.
- Add `LedgerService` protobuf contract with `GetEntries`.
- Add protobuf generation config and document the exact command.
- Add a shared Go event type for `tx.initiated` JSON.
- Add empty or doc-only package boundaries for upcoming gateway, wallet, and ledger service code if needed.
- Preserve Phase 1 API behavior.

## Out of Scope

- Running gRPC servers.
- Kafka producer or consumer implementation.
- Moving HTTP handlers.
- Changing database schema.
- Docker Compose changes.

## Files

- Create `services/proto/wallet/v1/wallet.proto`.
- Create `services/proto/ledger/v1/ledger.proto`.
- Create or update protobuf generation configuration in `services`.
- Create `services/internal/event/tx.go` for `TransactionInitiated`.
- Update `services/README.md` with proto generation instructions only if the command is not already documented elsewhere.

## Data Flow

No runtime data flow changes in this spec. Existing Phase 1 HTTP requests still use the monolith. Later specs will consume the contracts introduced here.

## Error Handling

Define contract comments and expected gRPC status mapping:

- `INVALID_ARGUMENT` for malformed input.
- `NOT_FOUND` for missing wallets.
- `FAILED_PRECONDITION` for insufficient funds.
- `INTERNAL` for unexpected persistence or infrastructure errors.

Actual mapping is implemented in later service specs.

## Testing

- Add tests for JSON marshal/unmarshal of the shared `TransactionInitiated` event.
- Run protobuf generation and ensure generated Go code compiles.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- Generated wallet and ledger protobuf Go packages compile.
- `TransactionInitiated` serializes with Phase 2 schema fields: `transfer_id`, `from_wallet_id`, `to_wallet_id`, `amount_cents`, `currency`, `initiated_at`.
- Existing Phase 1 tests still pass.
- No service behavior changes are introduced.

## Tradeoffs

Contracts first adds a small upfront step, but it prevents later sessions from making incompatible gateway, wallet, and ledger assumptions. JSON is kept for the Kafka event because Phase 2 explicitly defers protobuf event payloads to a later phase.

