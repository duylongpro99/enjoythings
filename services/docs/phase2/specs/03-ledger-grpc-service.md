# Phase 2.3: Ledger gRPC Service

**Priority:** P3 - ledger read boundary  
**Session size:** One implementation session  
**Depends on:** `00-contracts-and-layout.md`; `02-gateway-rest-to-grpc.md` preferred for gateway routing

## Goal

Expose ledger read queries through a standalone ledger gRPC service and prepare ledger storage for append-only event ownership.

## Problem

Phase 1 ledger reads are served by the single API. Phase 2 requires the gateway to query a ledger service and later requires ledger writes to come from Kafka. The read boundary can be extracted before the Kafka consumer exists.

## Structure

Create `services/cmd/ledger` as a gRPC service implementing `LedgerService.GetEntries`. Reuse the current ledger query SQL and repo behavior where possible. Keep ledger storage append-only: entries may be inserted, but existing entries must not be updated or deleted by service code. If current migrations already satisfy this, do not rewrite them.

## Scope

- Add ledger service configuration for gRPC address and database URL.
- Implement `GetEntries`.
- Add gateway ledger gRPC client and route `GET /v1/ledger/:id`.
- Preserve existing pagination semantics such as `limit` and cursor if present in Phase 1.
- Document any mismatch between current Phase 1 ledger schema and Phase 2 event-store wording.

## Out of Scope

- Kafka consumer.
- Wallet outbox.
- Ledger write conversion from synchronous transfer path.
- Redis read model.
- Saga behavior.

## Files

- Create `services/cmd/ledger/main.go`.
- Create `services/internal/ledgergrpc/server.go`.
- Create `services/internal/ledgergrpc/server_test.go`.
- Create `services/internal/gateway/client/ledger.go` if gateway exists.
- Modify `services/internal/gateway/handler/ledger.go` or equivalent gateway route file.
- Modify repo ledger query code only as needed to support the gRPC response.

## Data Flow

```
Client HTTP/JSON
  -> Gateway JWT middleware
  -> Gateway ledger handler
  -> Ledger gRPC client
  -> Ledger service
  -> Ledger repo
  -> Postgres
```

Direct ledger gRPC calls are also supported for tests and internal use.

## Error Handling

- Invalid wallet IDs or invalid pagination parameters return `INVALID_ARGUMENT` from ledger gRPC and HTTP `400` from gateway.
- Missing wallet or no entries should follow Phase 1 behavior. If Phase 1 returns an empty list for no entries, preserve that.
- Persistence errors return `INTERNAL` from gRPC and HTTP `500` from gateway.

## Testing

- Ledger gRPC tests cover valid query, invalid wallet ID, limit handling, and empty result.
- Gateway route tests cover HTTP-to-gRPC response mapping for ledger entries.
- Repo tests continue to cover deterministic entry order.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- `services/cmd/ledger` starts a gRPC server.
- Gateway `GET /v1/ledger/:id` routes to ledger gRPC.
- Ledger entries match Phase 1 response semantics.
- Ledger service code does not update or delete existing ledger entries.
- Existing Phase 1 tests still pass or are intentionally migrated to gateway/service equivalents.

## Tradeoffs

This separates ledger reads before async ledger writes. That gives the gateway a complete read path while keeping Kafka consumer idempotency isolated in a later spec.

