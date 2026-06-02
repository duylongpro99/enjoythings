# Phase 1 Spec: Testing and Verification

**Phase:** 1 - Monolith  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Define the verification strategy for Phase 1 so correctness is proven at the right boundaries: domain invariants, handler behavior, service rules, repository SQL, and end-to-end database transactions.

## Scope

Testing includes:

- Unit tests for domain, service, middleware, and handler packages.
- Repository and transaction integration tests against real Postgres.
- Concurrency tests for transfer safety.
- Static verification through `go vet` and `golangci-lint`.
- Docker Compose smoke verification.

Testing excludes performance/load testing, chaos testing, Kubernetes tests, browser tests, and external payment/fraud integrations.

## Test Layers

| Layer | Goal | Dependencies |
|---|---|---|
| Domain | Prove pure money and wallet invariants. | None |
| Middleware | Prove JWT extraction and validation behavior. | In-memory HTTP test server |
| Handler | Prove request parsing and response mapping. | Mock service interfaces |
| Service | Prove business rules and transaction orchestration. | Mock repo or fake transactional repo |
| Repository | Prove SQL and migrations work. | Real Postgres |
| Integration | Prove full flows across HTTP, service, repo, and DB. | Real Postgres |

## Required Commands

From `services/`:

```sh
go test ./...
go vet ./...
golangci-lint run
sqlc generate
```

Docker smoke path:

```sh
docker compose up --build
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

## Integration Test Database

Use `testcontainers-go` for automated integration tests. Tests must not depend on a developer's local Postgres instance.

Integration tests should:

- Start a Postgres 16 container.
- Run migrations before test cases.
- Use isolated test data per case.
- Clean up containers after completion.
- Avoid sleeping for readiness when container health checks can be used.

If testcontainers is unavailable in CI, provide a documented fallback using `DATABASE_URL` for an externally managed test database.

## Minimum Test Matrix

| Scenario | Required Level | Expected Result |
|---|---|---|
| Missing JWT | Handler/middleware | `401` |
| Expired JWT | Handler/middleware | `401` |
| Create wallet | Handler + integration | `201`, balance `0` |
| Read own wallet balance | Handler + integration | `200`, current balance |
| Read another user's wallet | Service + handler | `404` |
| Successful transfer | Integration | Both balances update and two ledger entries exist |
| Insufficient funds transfer | Integration | `422`, balances unchanged, no ledger entries |
| Concurrent transfers from same wallet | Integration | Final balance never negative |
| Ledger pagination | Repository + handler | Stable newest-first pages |
| Postgres unavailable for readiness | Handler | `/readyz` returns `503` |

## Mocking Strategy

Prefer small interfaces owned by consumers. Handlers should depend on service interfaces that contain only the methods they call. Services should depend on repository interfaces that reflect business operations, not broad generated sqlc types.

Hand-written fakes are acceptable and preferred when they stay simple. Use generated mocks only if hand-written fakes become repetitive.

## Fixtures

Test fixtures should use builders or helper functions for clarity:

- `newPrincipal(t)`
- `newWallet(t, userID, balance)`
- `signTestToken(t, claims)`
- `createWalletFixture(t, db, userID, balance)`

Avoid global mutable fixtures. Each test should own its setup.

## CI Expectations

A future CI workflow should run:

```sh
cd services
go test ./...
go vet ./...
golangci-lint run
sqlc generate
```

Until CI exists, the same commands are the local completion gate.

## Acceptance Criteria

- All handlers have unit tests with mocked services.
- Auth middleware has direct tests for valid and invalid tokens.
- Services have unit tests for business rules and domain errors.
- Repositories have integration tests against real Postgres.
- Transfer happy path and insufficient-funds path are covered by integration tests.
- Concurrency test proves row locking prevents negative balances.
- `go test ./...`, `go vet ./...`, and `golangci-lint run` pass before Phase 1 is considered complete.
