# EnjoyThings Services

Phase 1 services are a single Go API binary backed by Postgres.

## Local Development

Run a Go service locally in watch mode while dependencies stay in Docker:

```sh
cd services
go install github.com/air-verse/air@latest
make dev
```

`make dev` defaults to the API service and expands to `SERVICE=api make dev`. Any future Go service under `cmd/<name>` can use the same watcher:

```sh
cd services
SERVICE=api make dev
SERVICE=<name> make dev
```

The dev target starts Postgres with `docker-compose.dev.yml`, then runs `go run ./cmd/<service>` locally through `air`. It does not build or start the API container. Override ports or config with environment variables when needed:

```sh
cd services
HTTP_ADDR=:18080 POSTGRES_PORT=15432 SERVICE=api make dev
```

Start Postgres and the API:

```sh
cd services
docker compose up --build
```

If host port `8080` is already in use, keep the container on `8080` and change only the host port:

```sh
cd services
API_PORT=18080 docker compose up --build
curl http://localhost:18080/healthz
```

Health checks:

```sh
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Phase 1 API

Business endpoints are served under `/v1` and require a bearer JWT. Requests with a JSON body must include `Content-Type: application/json`.

Create a wallet:

```sh
curl -X POST http://localhost:8080/v1/wallets \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"currency":"USD"}'
```

Response `201`:

```json
{
  "id": "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c",
  "user_id": "449b3f19-8b5b-4f4b-b5d0-8e77d97d5c84",
  "balance": 0,
  "currency": "USD",
  "created_at": "2026-06-02T00:00:00Z",
  "updated_at": "2026-06-02T00:00:00Z"
}
```

Read a wallet and its balance:

```sh
curl -H "Authorization: Bearer $JWT" \
  http://localhost:8080/v1/wallets/6ed87f1f-7c9d-48d6-b23a-4d6255028c5c

curl -H "Authorization: Bearer $JWT" \
  http://localhost:8080/v1/wallets/6ed87f1f-7c9d-48d6-b23a-4d6255028c5c/balance
```

Balance response `200`:

```json
{
  "wallet_id": "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c",
  "balance": 1500,
  "currency": "USD"
}
```

Create a transfer:

```sh
curl -X POST http://localhost:8080/v1/transfers \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"from_wallet_id":"77787174-e221-49de-bf16-5834e0d250a1","to_wallet_id":"a572d276-3292-4c0e-b4f8-e5256d2d814c","amount":1250}'
```

Response `201`:

```json
{
  "id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
  "from_wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "to_wallet_id": "a572d276-3292-4c0e-b4f8-e5256d2d814c",
  "amount": 1250,
  "status": "completed",
  "created_at": "2026-06-02T00:00:00Z",
  "balances": {
    "from": 3750,
    "to": 6250
  }
}
```

List ledger entries:

```sh
curl -H "Authorization: Bearer $JWT" \
  "http://localhost:8080/v1/ledger/77787174-e221-49de-bf16-5834e0d250a1?limit=50"
```

Response `200`:

```json
{
  "wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "entries": [
    {
      "id": "bc1f6d24-ff58-4fc7-8493-c5d380035b79",
      "transfer_id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
      "direction": "debit",
      "amount": 1250,
      "balance_after": 3750,
      "created_at": "2026-06-02T00:00:00Z"
    }
  ],
  "next_cursor": null
}
```

Non-2xx responses use the standard error envelope:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "request body is invalid"
  }
}
```

Generate a local-development JWT for authenticated `/v1/*` requests:

```sh
cd services
JWT_SECRET=local-dev-jwt-secret-change-me go run ./cmd/devtoken \
  -user-id 6ed87f1f-7c9d-48d6-b23a-4d6255028c5c \
  -role user \
  -ttl 1h
```

`cmd/devtoken` is for local development and automated tests only. Do not commit or share generated tokens, and do not use local secrets outside local environments.

Run tests and static checks:

```sh
cd services
go test ./...
go vet ./...
golangci-lint run
sqlc generate
```

Generate protobuf Go and gRPC code after changing files under `proto/`:

```sh
cd services
buf generate
```

Install local tools when they are not already available:

```sh
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
go install github.com/air-verse/air@latest
```

Run migrations against the Compose database:

```sh
cd services
migrate -path db/migrations -database "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable" up
```

The Compose credentials are local development values only. Do not use them as production guidance.

## Configuration

The API loads configuration from environment variables at startup.

| Variable | Required | Default |
|---|---:|---|
| `APP_ENV` | No | `local` |
| `HTTP_ADDR` | No | `:8080` |
| `DATABASE_URL` | Yes | none |
| `JWT_SECRET` | Yes | none |
| `DB_MAX_CONNS` | No | `10` |

Missing required values or invalid `DB_MAX_CONNS` values fail startup.
