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
- OpenTelemetry traces, Prometheus metrics, Grafana dashboards, health checks,
  Docker Compose, Kubernetes manifests, and a Helm chart

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
make phase3-smoke
make phase4-observability
make wallet-rollout-test
```

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

## Documentation

- [`services/docs/prd.md`](services/docs/prd.md): platform product requirements
- [`services/docs/architecture.md`](services/docs/architecture.md): platform architecture
- [`services/docs/phase3/architecture.md`](services/docs/phase3/architecture.md):
  saga, verification, payments, notifications, and Kubernetes
- [`services/docs/phase4/architecture.md`](services/docs/phase4/architecture.md):
  fraud agent and observability
- [`services/docs/phase4/specs/README.md`](services/docs/phase4/specs/README.md):
  Phase 4 implementation contracts

## Security Notes

- Never commit `.env`, credentials, generated JWTs, audit databases, logs, or
  exported reports.
- The fraud prompt guard prevents raw wallet and user identifiers from reaching
  the configured LLM provider.
- Local JWT, database, Grafana, Kafka, and gRPC settings are not production
  security guidance.
