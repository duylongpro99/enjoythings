# Phase 2.1: Wallet gRPC Service

**Priority:** P1 - first behavioral extraction  
**Session size:** One implementation session  
**Depends on:** `00-contracts-and-layout.md`

## Goal

Expose Phase 1 wallet behavior through a standalone wallet gRPC service while preserving the existing wallet business invariants.

## Problem

The current API routes call wallet logic in-process. Phase 2 requires gateway-to-wallet calls over gRPC, but the wallet service should not learn HTTP concerns or duplicate domain logic.

## Structure

Create `services/cmd/wallet` as a gRPC server. Implement the generated `WalletServiceServer` in a wallet-specific internal package that adapts protobuf requests to the existing wallet application service. Keep wallet ownership over Postgres balances and transfer invariants. Authentication is not moved into the wallet service; downstream services trust the gateway for Phase 2, while user ownership data needed for wallet operations is passed explicitly in request metadata or request fields chosen in the contract.

## Scope

- Add wallet service configuration for gRPC address and database URL.
- Add gRPC server startup, health endpoints only if the existing service pattern requires them for local readiness.
- Implement `CreateWallet`, `GetBalance`, and `InitiateTransfer`.
- Reuse existing repo and wallet service logic where possible.
- Map existing domain errors to gRPC status codes.
- Keep Phase 1 API tests passing during the transition.

## Out of Scope

- Gateway implementation.
- Kafka publishing.
- Outbox table.
- Ledger service extraction.
- Redis read model.

## Files

- Create `services/cmd/wallet/main.go`.
- Create `services/internal/walletgrpc/server.go`.
- Create `services/internal/walletgrpc/server_test.go`.
- Modify `services/internal/config` only for reusable config parsing if needed.
- Modify `services/internal/wallet/service.go` only when an interface boundary is required for gRPC reuse.

## Data Flow

```
gRPC client
  -> WalletServiceServer
  -> existing wallet application service
  -> existing repo
  -> Postgres
```

`InitiateTransfer` still performs the Phase 1 synchronous transfer and ledger behavior until later specs introduce outbox and asynchronous ledger writing. If synchronous ledger behavior must be removed to satisfy ownership, replace it only when `05-ledger-kafka-consumer.md` is implemented.

## Error Handling

- Invalid IDs, unsupported currency, and non-positive amounts return `INVALID_ARGUMENT`.
- Missing wallets return `NOT_FOUND`.
- Insufficient funds returns `FAILED_PRECONDITION`.
- Database and unexpected errors return `INTERNAL` with a safe message.

## Testing

- Unit-test request validation and error mapping in the gRPC adapter.
- Use fake wallet application dependencies for adapter tests where practical.
- Keep existing wallet service and repo tests unchanged unless signatures must move.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- `services/cmd/wallet` starts a gRPC server.
- Wallet gRPC methods match the protobuf contract.
- Existing wallet invariants remain enforced: positive amount, wallet existence, ownership, currency consistency, non-negative balance.
- Domain errors are observable as correct gRPC status codes.
- Phase 1 tests still pass.

## Tradeoffs

This spec intentionally extracts wallet gRPC before Kafka. That keeps the first service split focused on network boundaries and avoids mixing transport bugs with event-delivery bugs.

