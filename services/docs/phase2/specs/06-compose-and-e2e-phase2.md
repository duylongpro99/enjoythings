# Phase 2.6: Compose and End-to-End Verification

**Priority:** P6 - final Phase 2 integration  
**Session size:** One implementation session  
**Depends on:** `02-gateway-rest-to-grpc.md`, `05-ledger-kafka-consumer.md`

## Goal

Wire the local Phase 2 stack so gateway, wallet, ledger, Kafka, and Postgres run together and prove the full transfer-to-ledger flow.

## Problem

Individual services can pass unit tests while local orchestration is broken. Phase 2 acceptance requires a client request to flow through gateway, wallet gRPC, Kafka, and ledger consumer.

## Structure

Update local Docker Compose to run Kafka, Zookeeper or a compatible single-node Kafka setup, gateway, wallet, ledger, and their databases. Add a repeatable smoke test path that creates wallets, initiates a transfer through the gateway, waits for ledger consumption, and queries ledger entries through the gateway.

## Scope

- Update `services/docker-compose.yml` and `services/docker-compose.dev.yml` if both are still used.
- Add Kafka service and topic creation strategy.
- Add gateway, wallet, and ledger service configuration.
- Wire `/healthz` and `/readyz` checks for gateway, wallet, and ledger using the service ports exposed by the Compose stack.
- Add a smoke test script or Go integration test for the Phase 2 happy path.
- Update `services/README.md` with local Phase 2 run commands.

## Out of Scope

- Kubernetes.
- Helm charts.
- Prometheus, Grafana, and Jaeger.
- Production secrets management.
- Multi-broker Kafka.

## Files

- Modify `services/docker-compose.yml`.
- Modify `services/docker-compose.dev.yml` if needed for local dependency startup.
- Modify `services/README.md`.
- Create `services/devtools/phase2_smoke_test.go` or a small script under `services/devtools`.
- Add Make targets only if they fit the existing `services/Makefile` pattern.

## Data Flow

```
Client smoke test
  -> Gateway HTTP
  -> Wallet gRPC
  -> Wallet Postgres and outbox
  -> Kafka tx.initiated
  -> Ledger consumer
  -> Ledger Postgres
  -> Gateway HTTP ledger query
```

## Error Handling

- Smoke test should fail fast on missing JWT, service unavailable, or timeout waiting for ledger entries.
- Compose should use local development credentials only.
- Documentation must warn that local secrets are not production guidance.

## Testing

- Run `go test ./...` from `services`.
- Run the Phase 2 smoke test against the Compose stack.
- Manually verify the ledger catch-up scenario if the smoke test does not automate restart: stop ledger, create transfer, restart ledger, query ledger.

## Acceptance Criteria

- One documented command sequence starts the Phase 2 local stack.
- Gateway transfer request reaches wallet service over gRPC.
- Wallet publishes `tx.initiated` through the outbox publisher.
- Ledger service consumes the event and appends entries.
- Gateway ledger query returns the new debit or credit entry.
- Invalid JWT returns `401` without reaching wallet.
- Insufficient funds returns `422`.

## Tradeoffs

Keeping orchestration until the final spec avoids forcing every earlier service-extraction session to debug Compose. The final integration spec is where cross-service timing, startup order, and local developer ergonomics belong.
