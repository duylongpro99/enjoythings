# Phase 2.2: Gateway REST to gRPC

**Priority:** P2 - client boundary split  
**Session size:** One implementation session  
**Depends on:** `01-wallet-grpc-service.md`

## Goal

Create an API Gateway that owns client HTTP, JWT validation, rate limiting, and REST-to-gRPC routing for wallet operations.

## Problem

Phase 2 moves authentication out of downstream services. The gateway must preserve Phase 1 REST behavior while changing the implementation path from in-process calls to wallet gRPC.

## Structure

Create `services/cmd/gateway` as the public HTTP service. Reuse existing HTTP response envelopes and JWT middleware where possible, but move wallet business calls behind a wallet gRPC client wrapper. Add an in-memory token-bucket limiter keyed by authenticated user ID. Keep the current `cmd/api` compiling during the transition unless the implementation session explicitly removes it after gateway parity is proven.

## Scope

- Add gateway configuration for HTTP address, JWT secret, wallet gRPC address, and rate-limit settings.
- Add wallet gRPC client wrapper.
- Port wallet REST routes: create wallet, get wallet, get balance, create transfer.
- Validate JWT at the gateway.
- Enforce per-user in-memory rate limiting.
- Convert gRPC status codes to existing HTTP error envelopes.

## Out of Scope

- Ledger query route unless `03-ledger-grpc-service.md` is also complete.
- Kafka, outbox, or asynchronous ledger behavior.
- Distributed tracing.
- Persistent or distributed rate limiting.

## Files

- Create `services/cmd/gateway/main.go`.
- Create `services/internal/gateway/client/wallet.go`.
- Create `services/internal/gateway/handler/wallet.go`.
- Create `services/internal/gateway/middleware/ratelimit.go`.
- Create tests under `services/internal/gateway/...`.
- Reuse or move code from `services/internal/handler` and `services/internal/auth` with minimal churn.

## Data Flow

```
Client HTTP/JSON
  -> Gateway JWT middleware
  -> Gateway rate limiter
  -> Gateway wallet handler
  -> Wallet gRPC client
  -> Wallet service
```

## Error Handling

- Missing or invalid JWT returns HTTP `401`; wallet gRPC is not called.
- Rate limit exceeded returns HTTP `429`.
- `INVALID_ARGUMENT` maps to HTTP `400`.
- `NOT_FOUND` maps to HTTP `404`.
- `FAILED_PRECONDITION` maps to HTTP `422`.
- `INTERNAL` maps to HTTP `500`.

All HTTP errors keep the Phase 1 envelope:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "request body is invalid"
  }
}
```

## Testing

- Handler tests prove invalid JWT short-circuits before gRPC.
- Handler tests prove gRPC errors map to the expected HTTP status and envelope.
- Rate-limiter tests cover allowed requests and `429` after token exhaustion.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- Gateway serves Phase 1 wallet REST endpoints through wallet gRPC.
- Invalid JWT returns `401`.
- Insufficient funds returns `422`.
- Response JSON remains compatible with Phase 1 clients.
- The wallet service contains no client-facing HTTP authentication logic for the gateway path.

## Tradeoffs

Keeping rate limiting in memory is acceptable for Phase 2 because the PRD explicitly defers production-grade distributed limits. The client wrapper isolates generated gRPC types from HTTP handlers so future gateway routes do not spread transport details everywhere.

