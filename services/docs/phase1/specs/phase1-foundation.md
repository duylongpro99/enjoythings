# Phase 1 Spec: Foundation

**Phase:** 1 - Monolith  
**Priority:** P0  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Create the runnable foundation for the Phase 1 Go monolith: project structure, configuration, database connection, migrations, generated queries, health endpoints, local Docker environment, and developer commands. This spec should be completed before feature specs because every capability depends on it.

## Scope

Foundation includes:

- Single Go API binary under `services/cmd/api`.
- Internal package layout for handlers, services, repositories, middleware, and domain types.
- Environment-driven configuration.
- Postgres connection via `pgx`.
- SQL migrations via `golang-migrate`.
- Type-safe query generation via `sqlc`.
- `/healthz` and `/readyz` endpoints.
- Local Docker Compose with API and Postgres.
- README instructions for local development.

Foundation excludes business endpoints for wallets, transfers, ledger, KYC, Kafka, gRPC, Redis, Kubernetes, and distributed tracing.

## Target Structure

```text
services/
|-- cmd/
|   `-- api/
|       `-- main.go
|-- internal/
|   |-- config/
|   |-- domain/
|   |-- handler/
|   |-- middleware/
|   |-- repo/
|   `-- service/
|-- db/
|   |-- migrations/
|   `-- query/
|-- docker-compose.yml
|-- Dockerfile
|-- go.mod
|-- go.sum
|-- sqlc.yaml
`-- README.md
```

The monolith should keep package boundaries clear even though it ships as one process. Handlers should not import database packages directly. Domain packages should not import HTTP or database packages.

## Configuration

Configuration is loaded from environment variables at startup:

| Variable | Required | Purpose | Local default |
|---|---:|---|---|
| `APP_ENV` | No | Runtime environment name | `local` |
| `HTTP_ADDR` | No | API listen address | `:8080` |
| `DATABASE_URL` | Yes | Postgres connection string | Compose-provided |
| `JWT_SECRET` | Yes | HMAC JWT signing secret for Phase 1 | Compose-provided |
| `DB_MAX_CONNS` | No | pgx pool max connections | `10` |

Do not commit `.env` files or real secrets. Safe defaults belong only in `.env.example` or Compose files for local-only development.

## Database Foundation

Use Postgres 16 locally. Enable UUID generation in the first migration with `pgcrypto`:

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;
```

Migrations must be idempotent where practical and reversible through matching `.down.sql` files. Schema details for wallets, transfers, and ledger entries are owned by their capability specs, but the foundation owns the migration runner setup and folder convention.

## Health Endpoints

### `GET /healthz`

Returns `200 OK` when the process is alive. It must not depend on Postgres.

Example response:

```json
{"status":"ok"}
```

### `GET /readyz`

Returns `200 OK` only when the process can ping Postgres through the configured pool. Returns `503 Service Unavailable` when the database is unavailable.

Example success response:

```json
{"status":"ready"}
```

Example failure response:

```json
{"error":{"code":"service_unavailable","message":"database is not ready"}}
```

## Docker Compose

Compose must start Postgres and the API. The API should wait for Postgres health before starting.

Required services:

- `postgres`: `postgres:16-alpine`, local port `5432`, health check with `pg_isready`.
- `api`: built from `services/Dockerfile`, local port `8080`, configured with `DATABASE_URL` and `JWT_SECRET`.

Local credentials may be simple development values, but they must not be reused as production guidance.

## Developer Commands

The README must document:

```sh
cd services
docker compose up --build
go test ./...
go vet ./...
golangci-lint run
sqlc generate
```

If `golangci-lint` or `sqlc` are not installed globally, the README should document the exact installation or containerized path chosen by the project.

## Error Handling

Foundation-level errors use the shared API error envelope:

```json
{
  "error": {
    "code": "service_unavailable",
    "message": "database is not ready"
  }
}
```

Startup should fail fast when required config is missing or invalid. Request-time errors should be logged with metadata, not credentials or raw secrets.

## Testing Requirements

- Config loading tests cover required variables, defaults, and invalid values.
- Health handler tests cover `/healthz`, ready success, and ready failure.
- Database integration test confirms a Postgres container can be pinged through the configured pool.
- Docker Compose smoke path starts API and Postgres, then `/healthz` returns `200`.

## Acceptance Criteria

- `docker compose up --build` starts Postgres and the API.
- `GET /healthz` returns `200` without requiring Postgres.
- `GET /readyz` returns `200` when Postgres is reachable.
- `GET /readyz` returns `503` when Postgres is not reachable.
- `sqlc generate` succeeds from committed query files once capability specs add SQL.
- `go test ./...` and `go vet ./...` pass for foundation packages.
- README contains reproducible local setup instructions.
