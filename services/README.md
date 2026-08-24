# EnjoyThings Services

Phase 3 services run as a local gateway, saga orchestrator, wallet, ledger,
payment processor, verification, notification, Kafka, and Postgres stack.

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

The dev target starts Postgres and Kafka with `docker-compose.dev.yml`, then runs `go run ./cmd/<service>` locally through `air`. It does not build or start Go service containers. Override ports or config with environment variables when needed:

```sh
cd services
HTTP_ADDR=:18080 POSTGRES_PORT=15432 SERVICE=api make dev
```

Start the full Phase 2 stack:

```sh
cd services
docker compose up -d --build
```

If host port `8080` is already in use, keep the container on `8080` and change only the host port:

```sh
cd services
GATEWAY_PORT=18080 docker compose up -d --build
curl http://localhost:18080/healthz
```

Health checks:

```sh
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8081/healthz
curl http://localhost:8081/readyz
curl http://localhost:8082/healthz
curl http://localhost:8082/readyz
```

Run the Phase 2 happy-path smoke test after the stack is healthy:

```sh
cd services
docker compose up -d --build
go run ./devtools/phase2_smoke
```

The smoke test creates wallets through the gateway, seeds a local source balance directly in Postgres, verifies invalid JWT returns `401`, verifies insufficient funds returns `422`, creates a transfer, and waits for the ledger service to consume `tx.initiated` and expose the debit entry through the gateway.

Manual ledger catch-up check:

```sh
cd services
docker compose up -d --build
docker compose stop ledger
USER_ID=11111111-1111-1111-1111-111111111111
go run ./devtools/phase2_smoke -user-id "$USER_ID" -skip-ledger-wait
docker compose restart ledger
go run ./devtools/phase2_smoke \
  -user-id "$USER_ID" \
  -expect-ledger-wallet-id "<from_wallet_id printed above>" \
  -expect-ledger-transfer-id "<transfer_id printed above>"
```

The Compose credentials and JWT secret are local development values only. Do not use them as production guidance.

## Phase 3 Resilience Tests

Run the fast acceptance suite, which connects the real Phase 3 components
through deterministic service and event boundaries:

```sh
cd services
make test-phase3-e2e
```

Run the deployed-stack smoke test after Compose is healthy:

```sh
cd services
docker compose up -d --build
make phase3-smoke
```

The smoke test proves unverified rejection, auto verification, saga completion,
terminal payment compensation, payment retry, Wallet balance outcomes, Ledger
reservation states, and terminal outbox events. The stub rail uses amount
`40901` for terminal failure and `50301` for one retry followed by success.

To validate a Wallet rolling restart, deploy the Helm chart, start the gateway
port-forward documented in the Kubernetes guide, create the authenticated
Wallet probe documented there, then run:

```sh
cd services
WALLET_PROBE_URL="http://localhost:18080/v1/wallets/$WALLET_ID" \
GATEWAY_TOKEN="$GATEWAY_TOKEN" \
  make wallet-rollout-test
```

The validator scales Wallet to two ready replicas, continuously requests
Gateway `/readyz` and the Wallet-backed API endpoint, restarts Wallet, and
fails if either boundary is interrupted.

## Phase 4 Fraud End-to-End Tests

Run the offline fraud acceptance suite. It needs no Compose stack, no Kafka, and
no external model provider:

```sh
cd services
make test-phase4-e2e
```

The target runs both halves:

- `make test-phase4-python` drives the real worker, LangGraph scoring graph,
  provider registry, and Kafka publisher against a deterministic
  OpenAI-compatible server started inside the test. It covers allow, flag, and
  block verdicts, one validator retry, two malformed responses, prompt and
  response identifier rejection, enrichment failure, duplicate scoring
  requests, provider switching by configuration only, audit contents, canonical
  action derivation, and audit database loss.
- `go test ./internal/phase4e2e` drives the saga orchestrator, its Kafka
  consumer, a fraud worker double that publishes the real event contract, and
  the notification service. It covers the fail-open publish order, the review
  transition, duplicate flagged events, a payment result racing review, orphan
  verdicts, and identifier-free fraud events.

Run the deployed-stack fraud and observability smoke after Compose is healthy:

```sh
cd services
docker compose --env-file ../.env up -d --build
make phase4-smoke
```

`make phase4-observability` starts the stack and runs both smokes. The Phase 4
smoke drives one payment, then waits at most 30 seconds per boundary for the
fraud audit session, healthy Prometheus targets and fraud series, one Jaeger
trace crossing `saga-orchestrator` and `fraud-worker`, and the provisioned
Grafana dashboards. It names the failed boundary and requires
`GRAFANA_ADMIN_PASSWORD`, so export the `.env` values first:

```sh
cd services
set -a && . ../.env && set +a
make phase4-smoke
```

The audit assertions read the fraud database on the host port `FRAUD_DB_PORT`
(`5433` by default).

## Phase 2 API

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
| `JWT_SECRET` | Yes for `api` and `gateway` | none |
| `GRPC_ADDR` | No for `wallet` and `ledger` | `:9090` for wallet, `:9091` for ledger |
| `WALLET_GRPC_ADDR` | No for `gateway` | `127.0.0.1:9090` |
| `LEDGER_GRPC_ADDR` | No for `gateway` | `127.0.0.1:9091` |
| `KAFKA_BROKERS` | No for `wallet` and `ledger` | `127.0.0.1:9092` |
| `WALLET_OUTBOX_TOPIC` | No for `wallet` | `tx.initiated` |
| `LEDGER_CONSUMER_TOPIC` | No for `ledger` | `tx.initiated` |
| `LEDGER_CONSUMER_GROUP_ID` | No for `ledger` | `ledger-service` |
| `LEDGER_CONSUMER_ENABLED` | No for `ledger` | `true` |
| `DB_MAX_CONNS` | No | `10` |

Missing required values or invalid `DB_MAX_CONNS` values fail startup.

## Phase 4 observability

Create a local `.env` from the repository `.env.example`, replace every
`change-me` value, and configure the local `LLM_PROVIDERS_JSON` provider. Then start the
complete stack and run the scoring scenario:

```sh
make phase4-observability
```

Prometheus is available at `http://localhost:9095`, Grafana at
`http://localhost:3001`, and Jaeger at `http://localhost:16686`. Grafana uses
`GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` from `.env`; anonymous admin
access is disabled. The provisioned System Overview, Saga Health, and Fraud Agent
dashboards query Prometheus only. The smoke scenario publishes
`fraud.score.requested`, allowing the fraud panels to populate.

`make phase4-observability` finishes by running `phase4-smoke`, which fails with
the offending boundary when audit persistence, Prometheus targets, trace
propagation, or dashboard provisioning is broken.
