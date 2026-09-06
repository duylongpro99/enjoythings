# EnjoyThings

EnjoyThings is a production-oriented fintech learning platform built around Go
microservices, event-driven payment orchestration, and a Python LangGraph fraud
agent. The repository also contains the FastAPI streaming chat and Next.js UI
used by the shared LLM provider layer.

The implemented platform includes:

- JWT-protected REST gateway backed by gRPC services
- Wallet balances, transfers, and an append-only ledger
- Verification-gated, idempotent payment sagas with compensation
- Kafka events and transactional outbox publishers
- Python fraud scoring with sanitized LLM prompts, validated verdicts, and a
  dedicated TimescaleDB audit store
- Operator resume and reject for payments held in fraud review
- Per-topic Kafka dead-letter topics for unparseable records
- HS256 or RS256 token verification
- OpenTelemetry traces, Prometheus metrics, Grafana dashboards, health checks,
  Docker Compose, Kubernetes manifests, and a Helm chart
- GitHub Actions CI covering the Go, Python, and web suites

## Architecture

```text
Client
  |
  v
Go API Gateway (REST, JWT, rate limiting)
  |
  +---- gRPC ----> Wallet -----------+
  +---- gRPC ----> Ledger            |
  +---- gRPC ----> Verification      |
  +---- gRPC ----> Saga Orchestrator |
                                      v
                                  Apache Kafka
                                      |
                   +------------------+------------------+
                   |                  |                  |
                   v                  v                  v
          Payment Processor   Notification Service   Python Fraud Worker
                   |                                     |
                   v                                     +--> Ledger gRPC
          Stub Payment Rail                              +--> Verification gRPC
                                                         +--> LLM provider
                                                         +--> TimescaleDB audit

All Go services and the Python fraud worker export telemetry to
Prometheus/Grafana and Jaeger.
```

### Main Components

| Component | Responsibility |
| --- | --- |
| Gateway | Authenticates and rate-limits REST requests, then routes them over gRPC |
| Wallet | Owns balances, applies debits and credits, and publishes outbox events |
| Ledger | Stores immutable accounting entries and fraud enrichment signals |
| Verification | Owns user verification state and decisions |
| Saga Orchestrator | Coordinates payments, compensation, and fraud-review state |
| Payment Processor | Executes idempotent payments against the local stub rail |
| Notification | Consumes terminal and review events |
| Fraud Worker | Enriches and scores transactions through LangGraph and the shared LLM adapter |
| FastAPI Chat | Exposes the shared LLM adapter as an OpenAI-style SSE chat endpoint |
| Next.js Web | Streams chat responses through the Vercel AI SDK |

## Quick Start

### Prerequisites

- Docker with Docker Compose
- An OpenAI-compatible LLM endpoint, such as Ollama or LiteLLM
- `uv` and Python 3.11+ for Python development
- Go 1.26 for Go development
- pnpm 11 for the optional web UI

### Run the Complete Platform

Create the local environment file and replace the placeholder credentials and
LLM settings:

```bash
cp .env.example .env
```

The default LLM URL uses `host.docker.internal:11434`, which lets the fraud
worker reach an Ollama-compatible endpoint running on the host.

Start the services, data stores, event bus, fraud worker, and observability
stack:

```bash
cd services
docker compose --env-file ../.env up -d --build
```

Check the gateway and run the deployed-stack smoke test:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz

cd services
make phase3-smoke
```

Stop the stack:

```bash
cd services
docker compose --env-file ../.env down
```

Compose values and credentials are for local development only.

## Local Endpoints

| Endpoint | URL |
| --- | --- |
| Gateway API | `http://localhost:8080` |
| Wallet health/metrics | `http://localhost:8081` |
| Ledger health/metrics | `http://localhost:8082` |
| Saga health/metrics | `http://localhost:8084` |
| Payment processor health/metrics | `http://localhost:8085` |
| Verification health/metrics | `http://localhost:8086` |
| Notification health/metrics | `http://localhost:8087` |
| Fraud worker metrics | `http://localhost:9101/metrics` |
| Jaeger UI | `http://localhost:16686` |
| Prometheus | `http://localhost:9095` |
| Grafana | `http://localhost:3001` |

Grafana credentials come from `GRAFANA_ADMIN_USER` and
`GRAFANA_ADMIN_PASSWORD` in `.env`. All Go services expose `/healthz`,
`/readyz`, and `/metrics`.

## Gateway API

Business endpoints require a bearer JWT. Generate a local token:

```bash
cd services
JWT=$(JWT_SECRET=local-dev-jwt-secret-change-me go run ./cmd/devtoken \
  -user-id 11111111-1111-1111-1111-111111111111 \
  -role user \
  -ttl 1h)
```

Implemented routes:

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/v1/wallets` | Create a wallet |
| `GET` | `/v1/wallets/{id}` | Read a wallet |
| `GET` | `/v1/wallets/{id}/balance` | Read a wallet balance |
| `POST` | `/v1/transfers` | Start an asynchronous payment saga |
| `GET` | `/v1/payments/{id}` | Read payment saga status |
| `POST` | `/v1/payments/{id}/fraud-review/resume` | Release a payment held in fraud review (admin) |
| `POST` | `/v1/payments/{id}/fraud-review/reject` | Refund and fail a payment held in fraud review (admin) |
| `GET` | `/v1/ledger/{wallet_id}` | List ledger entries |
| `POST` | `/v1/verification/submit` | Submit a verification decision |
| `GET` | `/v1/verification/status` | Read the authenticated user's verification status |

Example authenticated request:

```bash
curl -X POST http://localhost:8080/v1/wallets \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"currency":"USD"}'
```

See [`services/README.md`](services/README.md) for request examples, response
shapes, and smoke-test scenarios.

## Fraud Scoring

The Saga Orchestrator publishes `fraud.score.requested` without delaying the
payment path. The Python worker:

1. Creates a durable audit session.
2. Enriches the transaction through Ledger and Verification gRPC APIs.
3. Removes raw identifiers and rejects unsafe or oversized prompts.
4. Calls the configured provider through the shared `app.llm` adapter.
5. Validates the JSON verdict and retries malformed output once.
6. Publishes `fraud.flagged` or `fraud.error`.

Flagged active payments move to `FRAUD_REVIEW`; model or worker failures are
fail-open. Provider selection is configuration-only through
`LLM_PROVIDERS_JSON` and `LLM_DEFAULT_PROVIDER`.

## Development

### Python

```bash
uv sync
uv run pytest
uv run ruff check .
uv run mypy
```

Run the standalone chat API:

```bash
cp .env.example .env
uv run uvicorn app.main:app --reload
```

The chat endpoint is `POST http://127.0.0.1:8000/chat`.

### Next.js Chat UI

In a separate terminal:

```bash
cd web
cp .env.local.example .env.local
pnpm install
pnpm dev
```

Checks for the web app:

```bash
cd web
pnpm lint
pnpm typecheck
pnpm test
```

The UI runs at `http://localhost:3000` and proxies chat requests to FastAPI.

### Go Services

Run all Go tests and static checks:

```bash
cd services
go test ./...
go vet ./...
golangci-lint run
```

Run one Go service locally with Air while Postgres and Kafka remain in Docker:

```bash
cd services
go install github.com/air-verse/air@latest
SERVICE=api make dev
```

Useful integration commands:

```bash
cd services
make test-phase3-e2e
make test-phase4-e2e
make phase3-smoke
make phase4-observability
make phase4-smoke
make wallet-rollout-test
```

`make test-phase4-e2e` runs the offline fraud acceptance suite: the Python
scenarios against a deterministic OpenAI-compatible test server, then the Go
saga and fraud review scenarios. `make phase4-smoke` validates a running stack —
audit persistence, Prometheus targets, one saga-to-worker trace, and the
provisioned dashboards. See
[`services/README.md`](services/README.md) for the required environment.

Regenerate protobuf clients after changing `services/proto`:

```bash
cd services
buf generate
make generate-python-grpc
```

## Kubernetes

The local Kubernetes deployment uses Kind and the Helm chart under
`services/charts/enjoythings`.

```bash
cd services
kind create cluster --name enjoythings --config k8s/kind/cluster.yaml
helm upgrade --install enjoythings charts/enjoythings \
  --namespace enjoythings \
  --create-namespace \
  -f charts/enjoythings/values-local.yaml
```

See
[`services/docs/phase3/kubernetes-local-guide.md`](services/docs/phase3/kubernetes-local-guide.md)
for image loading, port forwarding, rollout validation, and cleanup.

## Repository Layout

```text
.
├── app/                    # FastAPI, shared LLM adapters, and Python fraud worker
├── tests/                  # Python unit and integration tests
├── web/                    # Next.js streaming chat UI
├── services/
│   ├── cmd/                # Go service entrypoints
│   ├── internal/           # Go domain and transport implementations
│   ├── proto/              # gRPC and event contracts
│   ├── db/                 # Go service database migrations and queries
│   ├── devtools/           # Smoke and resilience validators
│   ├── charts/             # Helm chart
│   ├── k8s/                # Kind configuration
│   ├── observability/      # Prometheus and Grafana provisioning
│   └── docs/               # PRDs, architecture, specs, and guides
└── docs/                   # Cross-project plans and engineering lessons
```

## Fraud Review Decisions

A saga that the fraud worker flags moves to `FRAUD_REVIEW` and stays there until
an operator decides. Both routes require an `admin` `role` claim and record the
acting user in the fraud audit trail. Mint a local admin token with
`go run ./cmd/devtoken -user-id <uuid> -role admin -ttl 1h`.

```bash
# Release the payment. A payment result that arrived during the review is
# applied immediately; otherwise the saga waits for it as usual.
curl -X POST "$GATEWAY/v1/payments/$PAYMENT_ID/fraud-review/resume" \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H 'content-type: application/json' \
  -d '{"reason":"manual check cleared"}'

# Refund the payer and fail the saga with failure code fraud_rejected.
curl -X POST "$GATEWAY/v1/payments/$PAYMENT_ID/fraud-review/reject" \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H 'content-type: application/json' \
  -d '{"reason":"confirmed fraud"}'
```

### Review Deadline

Set `SAGA_FRAUD_REVIEW_TTL` on the saga orchestrator to a duration such as `24h`
and a reaper sweeps every `SAGA_FRAUD_REVIEW_REAPER_INTERVAL` (default `1m`),
rejecting any review older than the TTL through the same path as a manual
rejection. The audit record names the actor `system:fraud-review-reaper`, and
the `fraud_review_expired` saga event counts each one. Leave the TTL unset to
keep reviews open until an operator decides them.

## Dead-Letter Topics

Every consumer publishes a record it cannot parse to `<topic>.dlq` before it
commits the offset, so a broker outage retries the record instead of dropping
it. The payload carries the raw key and value, the source topic, partition and
offset, the decode error, and the failure time. Compose and the Helm chart
provision one dead-letter topic per consumed topic.

`dlq-redrive` works through a dead-letter topic in order. It reads with its own
consumer group, so a record stays pending until a decision commits it.

```bash
cd services
# Show what is waiting on payment.completed.dlq without deciding anything.
go run ./cmd/dlq-redrive list --topic payment.completed

# Put the next pending record back on payment.completed.
go run ./cmd/dlq-redrive redrive --topic payment.completed --max 1

# Replay the next record with a corrected value instead of the poison bytes.
go run ./cmd/dlq-redrive redrive --topic payment.completed --max 1 --value-file fixed.json

# Drop the next pending record. Without --max, decisions apply to every pending record.
go run ./cmd/dlq-redrive discard --topic payment.completed --max 1
```

Brokers come from `--brokers` or `KAFKA_BROKERS`. Each replayed record carries an
`x-dead-letter-redrive` header naming the dead-letter topic, partition, and offset
it came from.

## Token Verification

`JWT_ALG` selects the accepted signing method, and the services accept only that
one, so a token signed with anything else is rejected before its claims are
read.

| Setting | Purpose |
| --- | --- |
| `JWT_ALG=HS256` (default) | Verifies with the shared `JWT_SECRET`. Local development. |
| `JWT_ALG=RS256` | Verifies with an issuer public key from `JWT_PUBLIC_KEY_PEM` or `JWT_PUBLIC_KEY_FILE`. No shared secret needed. |

## Documentation

- [`services/docs/prd.md`](services/docs/prd.md): platform product requirements
- [`services/docs/architecture.md`](services/docs/architecture.md): platform architecture
- [`services/docs/phase3/architecture.md`](services/docs/phase3/architecture.md):
  saga, verification, payments, notifications, and Kubernetes
- [`services/docs/phase4/architecture.md`](services/docs/phase4/architecture.md):
  fraud agent and observability
- [`services/docs/phase4/specs/README.md`](services/docs/phase4/specs/README.md):
  Phase 4 implementation contracts
- [`services/docs/design-notes/phase5-operability-debt.md`](services/docs/design-notes/phase5-operability-debt.md):
  fraud review exit, dead letters, and RS256
- [`services/docs/design-notes/phase5-review-reaper-and-redrive.md`](services/docs/design-notes/phase5-review-reaper-and-redrive.md):
  fraud review deadline and dead-letter redrive
- [`services/docs/phase5/backlog.md`](services/docs/phase5/backlog.md):
  what is still deliberately deferred

## Security Notes

- Never commit `.env`, credentials, generated JWTs, audit databases, logs, or
  exported reports.
- The fraud prompt guard prevents raw wallet and user identifiers from reaching
  the configured LLM provider.
- Local JWT, database, Grafana, Kafka, and gRPC settings are not production
  security guidance.
- Internal gRPC between services still relies on the trusted-network
  assumption. mTLS and workload identity are tracked in
  [`services/docs/phase5/backlog.md`](services/docs/phase5/backlog.md).
