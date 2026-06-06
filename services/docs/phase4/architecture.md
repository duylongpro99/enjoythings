# Architecture — Phase 4: Python Fraud Agent + Full Observability

**Phase:** 4 of 4
**Last updated:** 2026-06-06
**Agent runtime:** Python FastAPI app package + LangGraph

---

## 1. System diagram

```text
Kafka: fraud.score.requested
        |
        v
  Python Fraud Worker (app/fraud)
  +--------------------------------------------------+
  | Kafka Consumer                                   |
  |    | deserialize FraudScoreRequest               |
  |    v                                             |
  | FraudAgent Graph (LangGraph)                     |
  | +----------------------------------------------+ |
  | | Build session state                          | |
  | |      |                                       | |
  | |      v                                       | |
  | | Enrich transaction                           | |
  | |      | calls FraudDataPort                   | |
  | |      v                                       | |
  | | Input guard: PII + token budget              | |
  | |      |                                       | |
  | |      v                                       | |
  | | LLM adapter: app.llm LLMPort                 | |
  | |      |                                       | |
  | |      v                                       | |
  | | Output validator: schema + retry             | |
  | |      |                                       | |
  | |      v                                       | |
  | | Persist audit + publish verdict              | |
  | +----------------------------------------------+ |
  |                                                  |
  | FraudDataPort                                    |
  |    |                                             |
  |    v                                             |
  | Thin gRPC Client Layer                           |
  |    | protobuf details stay here                  |
  +----+---------------------------------------------+
       |
       v
  Go services: wallet / ledger / verification / saga

Kafka outputs:
  - fraud.flagged
  - fraud.error
  - optional tx.paused through Saga Orchestrator
```

---

## 2. Core architecture decisions

| Concern | Decision |
|---|---|
| Fraud runtime | Python worker in the existing `app` package |
| Agent workflow | LangGraph graph with explicit nodes |
| Model abstraction | Existing `app.llm` `LLMPort` and `DriverRegistry` |
| Provider config | `LLM_PROVIDERS_JSON` and `LLM_DEFAULT_PROVIDER` |
| Go integration | Domain service interface plus thin gRPC client |
| Protobuf imports | Only in `app/fraud/integrations/grpc_client.py` |
| Audit persistence | Python repository writes `fraud_sessions` |
| Kafka integration | Python worker consumes `fraud.score.requested`, publishes `fraud.flagged` / `fraud.error` |
| Observability | OpenTelemetry in both Python and Go, Prometheus metrics, Grafana dashboards |

---

## 3. Folder structure

```text
app/
├── main.py                         # Existing FastAPI app; exposes /chat and /metrics
├── llm/                            # Existing provider abstraction
│   ├── ports.py
│   ├── registry.py
│   ├── service.py
│   └── drivers/
└── fraud/
    ├── __init__.py
    ├── config.py                   # Fraud env config
    ├── worker.py                   # Kafka worker entrypoint
    ├── graph.py                    # LangGraph construction
    ├── instruction.py              # System instruction constant
    ├── guards.py                   # PII guard and token budget checks
    ├── validator.py                # FraudVerdict parser/schema validator
    ├── service.py                  # FraudScoringService orchestration facade
    ├── ports.py                    # FraudDataPort, FraudPublisher, FraudSessionStore
    ├── dto.py                      # Domain DTOs used by agent nodes
    ├── metrics.py                  # Prometheus counters/histograms
    ├── tracing.py                  # Python OTel helpers
    ├── integrations/
    │   ├── grpc_client.py          # Thin gRPC client; owns protobuf imports
    │   ├── kafka_consumer.py       # aiokafka/confluent consumer wrapper
    │   └── kafka_publisher.py      # fraud.flagged / fraud.error producer
    └── repo/
        └── sessions.py             # fraud_sessions writes

services/
├── proto/                          # Add narrow fraud-enrichment RPCs if needed
├── internal/                       # Go service implementations remain owners
└── db/migrations/                  # fraud_sessions schema
```

---

## 4. Python domain ports

The graph depends on domain ports, not concrete transports.

```python
# app/fraud/ports.py
from typing import Protocol

from app.fraud.dto import (
    FraudSession,
    FraudVerdict,
    KYCStatus,
    TransactionHistoryEntry,
    FraudScoreRequest,
    VelocityMetrics,
)


class FraudDataPort(Protocol):
    async def get_transaction_history(
        self,
        wallet_ref: str,
        limit: int,
    ) -> list[TransactionHistoryEntry]:
        ...

    async def get_kyc_status(self, user_ref: str) -> KYCStatus:
        ...

    async def get_velocity_metrics(self, wallet_ref: str) -> VelocityMetrics:
        ...


class FraudSessionStore(Protocol):
    async def create(self, event: FraudScoreRequest) -> FraudSession:
        ...

    async def append_event(self, session_id: str, event: dict) -> None:
        ...

    async def complete(
        self,
        session_id: str,
        raw_response: str | None,
        verdict: FraudVerdict | None,
        outcome: str,
    ) -> None:
        ...


class FraudPublisher(Protocol):
    async def publish_flagged(
        self,
        event: FraudScoreRequest,
        verdict: FraudVerdict,
        session_id: str,
    ) -> None:
        ...

    async def publish_error(
        self,
        event: FraudScoreRequest,
        reason: str,
        session_id: str,
    ) -> None:
        ...
```

The `FraudDataPort` implementation can call many Go services internally, but the graph only sees one stable domain interface.

---

## 5. Thin gRPC client layer

```python
# app/fraud/integrations/grpc_client.py
class GrpcFraudDataClient:
    """Maps fraud-domain requests to Go service RPCs.

    This is the only Python fraud module that imports generated protobuf code.
    """

    async def get_transaction_history(self, wallet_ref: str, limit: int):
        # Resolve wallet_ref and call a ledger/wallet fraud-enrichment RPC.
        # Return app.fraud.dto.TransactionHistoryEntry objects only.
        ...

    async def get_kyc_status(self, user_ref: str):
        # Call verification service and return app.fraud.dto.KYCStatus.
        ...

    async def get_velocity_metrics(self, wallet_ref: str):
        # Call ledger/wallet fraud-enrichment RPC and return VelocityMetrics.
        ...
```

Rules:

- Generated protobuf imports are confined to this layer.
- gRPC retries, deadlines, metadata, and trace propagation live here.
- DTO mapping happens here.
- Agent nodes do not construct protobuf messages.
- Unit tests for graph behavior use a fake `FraudDataPort`.

---

## 6. LangGraph workflow

```text
create_session
  -> build_sanitised_context
  -> enrich_transaction
  -> build_prompt
  -> input_guard
  -> call_llm
  -> validate_verdict
  -> persist_audit
  -> publish_verdict
```

Node responsibilities:

| Node | Responsibility |
|---|---|
| `create_session` | Persist initial session and opaque wallet/user references that are safe for prompts |
| `build_sanitised_context` | Remove raw UUIDs from the LLM-facing context |
| `enrich_transaction` | Call `FraudDataPort` methods and store sanitized summaries |
| `build_prompt` | Assemble system instruction and user facts |
| `input_guard` | Reject raw UUIDs, sensitive keys, and oversized prompts before LLM call |
| `call_llm` | Invoke existing `app.llm` provider abstraction |
| `validate_verdict` | Parse `FraudVerdict`, validate action/score, retry malformed output once |
| `persist_audit` | Write raw response, parsed verdict, callback events, and outcome |
| `publish_verdict` | Publish `fraud.flagged` for flag/block, publish `fraud.error` on fail-open errors |

---

## 7. LLM adapter integration

Phase 4 keeps provider selection behind the existing adapter registry.

Current repository contract:

```python
class LLMPort(Protocol):
    async def stream_chat(self, request: ChatRequest) -> AsyncIterator[ChatDelta]:
        """Yield provider-agnostic content deltas."""
```

Fraud scoring needs a complete JSON response. Implement this by adding a small helper around the existing stream:

```python
class CompletionService:
    def __init__(self, llm_port: LLMPort) -> None:
        self._llm_port = llm_port

    async def complete(self, messages: list[ChatMessage]) -> str:
        chunks: list[str] = []
        async for delta in self._llm_port.stream_chat(ChatRequest(messages=messages)):
            chunks.append(delta.content)
        return "".join(chunks)
```

This avoids a second provider abstraction. If future drivers support non-streaming calls directly, `LLMPort` can be extended deliberately while keeping fraud logic unchanged.

Provider configuration remains:

```dotenv
LLM_PROVIDERS_JSON='{"providers":[{"id":"local","driver_type":"openai_compatible","base_url":"http://127.0.0.1:11434/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"llama3.1","timeout_seconds":30}]}'
LLM_DEFAULT_PROVIDER=local
LOCAL_LLM_API_KEY=replace-me
```

---

## 8. System instruction

```python
# app/fraud/instruction.py
SYSTEM_INSTRUCTION = """
You are a fraud detection engine for a fintech payment platform.

Use only the sanitized transaction facts and enrichment summaries provided.
Never infer or request wallet IDs, user IDs, names, emails, addresses, or raw identifiers.

Respond ONLY with a valid JSON object matching this schema.
Do not include markdown, code fences, or any explanation:

{
  "risk_score": <float 0.0-1.0>,
  "action":     <"allow" | "flag" | "block">,
  "reason":     "<one concise sentence>"
}

Risk scoring rubric:
  0.0-0.40  Normal patterns - allow
  0.40-0.74 Elevated risk - allow but log
  0.75-0.89 High risk - flag for review
  0.90-1.0  Critical risk - block

Signals that increase risk score:
  - Transaction amount > 3x the sender's 30-day average
  - Sender KYC status is unverified or pending
  - More than 5 transactions in the last hour
  - First transaction to this recipient category
  - Unusual amount pattern such as round numbers above $5000

Signals that decrease risk score:
  - Sender has verified KYC
  - Transaction amount is within historical normal range
  - Consistent transaction pattern over 30+ days
"""
```

---

## 9. Input guard

```python
UUID_PATTERN = re.compile(
    r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
)

SENSITIVE_KEYS = {
    "from_wallet_id",
    "to_wallet_id",
    "wallet_id",
    "user_id",
    "email",
    "name",
}


def validate_prompt(prompt: str, *, max_chars: int) -> None:
    if UUID_PATTERN.search(prompt):
        raise PromptRejected(reason="uuid_detected")
    lowered = prompt.lower()
    for key in SENSITIVE_KEYS:
        if key in lowered:
            raise PromptRejected(reason=f"sensitive_key:{key}")
    if len(prompt) > max_chars:
        raise PromptRejected(reason="token_budget_exceeded")
```

The guard runs immediately before the LLM adapter call. On rejection, the provider is not called.

---

## 10. Output validator

```python
class FraudVerdict(BaseModel):
    risk_score: float = Field(ge=0.0, le=1.0)
    action: Literal["allow", "flag", "block"]
    reason: str = Field(min_length=1, max_length=300)
```

Validation behavior:

- Parse model output as JSON.
- Reject non-object JSON.
- Require `risk_score`, `action`, and `reason`.
- Clamp only when a numeric risk score is slightly outside range; otherwise reject.
- Retry malformed output once with a corrective prompt.
- On second failure, publish `fraud.error`, persist an audit row, and fail open.

---

## 11. Kafka consumer and verdict publication

Consumer flow:

```python
async def handle_fraud_score_requested(event: FraudScoreRequest) -> None:
    session = await session_store.create(event)
    try:
        verdict = await fraud_service.score(event, session_id=session.id)
    except FraudFailOpen as exc:
        await publisher.publish_error(event, reason=str(exc), session_id=session.id)
        return

    if verdict.action in {"flag", "block"}:
        await publisher.publish_flagged(event, verdict, session_id=session.id)
```

Topics:

| Topic | Producer | Consumer |
|---|---|---|
| `fraud.score.requested` | Saga Orchestrator | Python fraud worker |
| `tx.initiated` | Wallet outbox | Ledger consumer only; Phase 2 compatibility |
| `fraud.flagged` | Python fraud worker | Saga Orchestrator, Notification |
| `fraud.error` | Python fraud worker | Observability/admin handling |
| `tx.paused` | Saga Orchestrator | Notification |

---

## 12. Saga integration

Add saga state:

```go
const StateFraudReview = "FRAUD_REVIEW"
```

Fraud event handling:

- `fraud.flagged` contains `payment_id` or enough correlation to find the saga.
- If saga state is `PAYMENT_PROCESSING`, update state to `FRAUD_REVIEW`.
- Emit `tx.paused` with fraud reason and session ID.
- If saga is terminal, record/ignore without state mutation.
- Resume/reject can be stubbed for Phase 4.

This design keeps fraud verdicts asynchronous and avoids making the payment path wait on the LLM.

---

## 13. TimescaleDB schema

```sql
CREATE TABLE fraud_sessions (
    session_id       UUID NOT NULL,
    transfer_id      UUID NOT NULL,
    payment_id       UUID,
    provider_id      TEXT NOT NULL,
    model_id         TEXT NOT NULL,
    enrichment       JSONB NOT NULL DEFAULT '{}',
    raw_llm_response TEXT,
    verdict          JSONB,
    callback_events  JSONB NOT NULL DEFAULT '[]',
    outcome          TEXT NOT NULL,
    scored_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, scored_at)
);

SELECT create_hypertable('fraud_sessions', 'scored_at');
```

Analytics examples:

```sql
SELECT
    provider_id,
    time_bucket('1 hour', scored_at) AS bucket,
    COUNT(*) FILTER (WHERE verdict->>'action' = 'flag') AS flagged,
    COUNT(*) AS total,
    AVG((verdict->>'risk_score')::float) AS avg_score
FROM fraud_sessions
WHERE scored_at > now() - interval '24 hours'
GROUP BY provider_id, bucket
ORDER BY bucket;
```

---

## 14. OpenTelemetry instrumentation

Python spans:

- `kafka.consume.tx_initiated`
- `fraud.session.create`
- `fraud.graph.enrich_transaction`
- `fraud.grpc.get_transaction_history`
- `fraud.grpc.get_kyc_status`
- `fraud.grpc.get_velocity_metrics`
- `fraud.input_guard`
- `fraud.llm.complete`
- `fraud.output_validate`
- `fraud.publish_verdict`

Span attributes:

| Attribute | Value |
|---|---|
| `fraud.session_id` | Audit session ID |
| `fraud.transfer_id` | Transfer ID |
| `fraud.provider_id` | `LLM_DEFAULT_PROVIDER` value |
| `fraud.verdict_action` | `allow`, `flag`, or `block` |
| `messaging.kafka.topic` | Kafka topic |

Trace context rules:

- Read `traceparent` from Kafka headers.
- Propagate trace metadata into gRPC calls.
- Publish `traceparent` on fraud output events.
- Go services preserve trace context across gRPC and Kafka boundaries.

---

## 15. Prometheus metrics

Fraud worker metrics:

| Metric | Type | Labels |
|---|---|---|
| `fraud_transactions_scored_total` | Counter | `action`, `provider` |
| `fraud_model_latency_seconds` | Histogram | `provider`, `model` |
| `fraud_risk_score` | Histogram | none |
| `fraud_enrichment_calls_total` | Counter | `method`, `outcome` |
| `fraud_callback_rejections_total` | Counter | `callback`, `reason` |
| `fraud_session_duration_seconds` | Histogram | `outcome` |
| `fraud_events_published_total` | Counter | `topic`, `outcome` |

The Python metrics endpoint may be exposed by FastAPI or by a worker-local metrics HTTP server.

---

## 16. Docker Compose additions

```yaml
  jaeger:
    image: jaegertracing/all-in-one:1.57
    ports:
      - "16686:16686"
      - "4318:4318"

  prometheus:
    image: prom/prometheus:v2.51.0
    volumes:
      - ./config/prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9091:9090"

  grafana:
    image: grafana/grafana:10.4.0
    volumes:
      - ./config/grafana/provisioning:/etc/grafana/provisioning
      - ./charts/grafana/dashboards:/var/lib/grafana/dashboards
    ports:
      - "3000:3000"

  timescaledb:
    image: timescale/timescaledb:latest-pg16
    environment:
      POSTGRES_DB: fraud
      POSTGRES_USER: fraud
      POSTGRES_PASSWORD: secret
    ports:
      - "5433:5432"

  fraud-worker:
    build:
      context: ..
      dockerfile: Dockerfile.python
    command: ["uv", "run", "python", "-m", "app.fraud.worker"]
    environment:
      KAFKA_BROKERS: kafka:9092
      FRAUD_CONSUMER_GROUP: fraud-agent
      FRAUD_SCORE_THRESHOLD: "0.75"
      FRAUD_PROMPT_MAX_CHARS: "8000"
      FRAUD_DATABASE_URL: postgres://fraud:secret@timescaledb:5432/fraud
      LEDGER_GRPC_ADDR: ledger:9091
      VERIFICATION_GRPC_ADDR: verification:9094
      OTEL_EXPORTER_OTLP_ENDPOINT: http://jaeger:4318
      LLM_PROVIDERS_JSON: ${LLM_PROVIDERS_JSON}
      LLM_DEFAULT_PROVIDER: ${LLM_DEFAULT_PROVIDER}
      LOCAL_LLM_API_KEY: ${LOCAL_LLM_API_KEY}
```

---

## 17. Final repository additions

```text
app/fraud/
├── config.py
├── dto.py
├── graph.py
├── guards.py
├── instruction.py
├── metrics.py
├── ports.py
├── service.py
├── tracing.py
├── validator.py
├── worker.py
├── integrations/
│   ├── grpc_client.py
│   ├── kafka_consumer.py
│   └── kafka_publisher.py
└── repo/
    └── sessions.py

services/proto/
└── fraud-enrichment RPC additions where needed

services/internal/saga/
└── fraud.flagged consumer and FRAUD_REVIEW transition

services/db/migrations/
└── fraud_sessions migration

services/config/
└── prometheus.yml and Grafana provisioning
```
