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

Run tests and static checks:

```sh
cd services
go test ./...
go vet ./...
golangci-lint run
sqlc generate
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
