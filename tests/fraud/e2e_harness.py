"""Shared assembly for Phase 4 fraud end-to-end scenarios.

Scenarios wire the production worker, scoring graph, provider registry, and Kafka
publisher together and vary only the deterministic fake provider, the enrichment
source, and the audit store.
"""

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime

from prometheus_client import CollectorRegistry, generate_latest

from app.fraud.completion import CompletionService
from app.fraud.config import FraudConfig
from app.fraud.dto import KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.graph import InMemoryFraudGraphSessionStore
from app.fraud.integrations.kafka import KafkaFraudPublisher
from app.fraud.metrics import FraudMetrics
from app.fraud.service import FraudScoringService
from app.fraud.worker import ConsumerDecision, FraudWorker
from app.llm.config import load_provider_registry_config
from app.llm.registry import DriverRegistry
from tests.fraud.fake_provider import FakeProviderServer, running_provider_server

PRIMARY_PROVIDER = "primary"
SECONDARY_PROVIDER = "secondary"
PAYMENT_ID = "11111111-1111-1111-1111-111111111111"
USER_ID = "22222222-2222-2222-2222-222222222222"
FROM_WALLET_ID = "33333333-3333-3333-3333-333333333333"
TO_WALLET_ID = "44444444-4444-4444-4444-444444444444"
TRACE_ID = "0af7651916cd43dd8448eb211c80319c"
RAW_IDENTIFIERS = (USER_ID, FROM_WALLET_ID, TO_WALLET_ID)
AUDIT_DATABASE_URL = "postgres://fraud:fraud@127.0.0.1:5432/fraud_audit"

ALLOW_RESPONSE = '{"risk_score":0.10,"action":"allow","reason":"amount matches history"}'
FLAG_RESPONSE = '{"risk_score":0.80,"action":"flag","reason":"velocity spike"}'
BLOCK_RESPONSE = '{"risk_score":0.95,"action":"block","reason":"velocity spike and new recipient"}'
MALFORMED_RESPONSE = "not json at all"
UUID_RESPONSE = (
    '{"risk_score":0.80,"action":"flag","reason":"wallet '
    f'{FROM_WALLET_ID} is risky"}}'
)


@dataclass(frozen=True)
class PublishedEvent:
    topic: str
    key: str
    payload: str

    @property
    def event(self) -> dict:
        return json.loads(self.payload)


@dataclass
class ScenarioResult:
    decisions: list[ConsumerDecision]
    published: list[PublishedEvent]
    store: object
    server: FakeProviderServer
    metrics_registry: CollectorRegistry

    @property
    def decision(self) -> ConsumerDecision:
        return self.decisions[-1]

    @property
    def topics(self) -> list[str]:
        return [event.topic for event in self.published]

    def events_on(self, topic: str) -> list[dict]:
        return [event.event for event in self.published if event.topic == topic]

    def audit_events(self) -> list[dict]:
        sessions = getattr(self.store, "events", {})
        return [event for events in sessions.values() for event in events]

    def audit_nodes(self) -> list[str]:
        return [str(event.get("node", "")) for event in self.audit_events()]

    def audit_event(self, node: str, **match: object) -> dict | None:
        for event in self.audit_events():
            if event.get("node") != node:
                continue
            if all(event.get(key) == value for key, value in match.items()):
                return event
        return None

    @property
    def metrics_text(self) -> str:
        return generate_latest(self.metrics_registry).decode()

    def sensitive_text(self) -> str:
        """Everything that leaves the worker: prompts, events, and audit rows."""

        prompts = [
            request.text
            for provider_id in self.server.provider_ids
            for request in self.server.requests(provider_id)
        ]
        return "\n".join([*prompts, *(event.payload for event in self.published)])


class RecordingProducer:
    def __init__(self) -> None:
        self.records: list[PublishedEvent] = []

    async def send_and_wait(self, topic, value, key=None, headers=None) -> None:
        self.records.append(
            PublishedEvent(
                topic=topic,
                key=(key or b"").decode(),
                payload=value.decode(),
            )
        )


class StubFraudData:
    """Enrichment source standing in for the Ledger and Verification services."""

    def __init__(
        self,
        *,
        kyc_status: str = "verified",
        history: Sequence[TransactionHistoryEntry] | None = None,
        velocity: VelocityMetrics | None = None,
        unavailable: bool = False,
    ) -> None:
        self._kyc_status = kyc_status
        self._history = list(history) if history is not None else [
            TransactionHistoryEntry("outbound", 2500, "USD", datetime(2026, 6, 1, tzinfo=UTC))
        ]
        self._velocity = velocity or VelocityMetrics(
            transactions_last_hour=9,
            amount_last_hour_cents=45000,
            average_amount_30d_cents=2500,
            distinct_recipients_30d=4,
        )
        self._unavailable = unavailable
        self.calls = 0

    async def get_transaction_history(self, wallet_id, limit, trace_id):
        self._record()
        return self._history

    async def get_velocity_metrics(self, wallet_id, trace_id):
        self._record()
        return self._velocity

    async def get_kyc_status(self, user_id, trace_id):
        self._record()
        return KYCStatus(self._kyc_status)

    def _record(self) -> None:
        self.calls += 1
        if self._unavailable:
            raise ConnectionError("enrichment backend unavailable")


class FailingAuditStore:
    """Audit store that accepts the claim and then loses the database."""

    def __init__(self, inner=None, *, fail_after: int = 1) -> None:
        self._inner = inner or InMemoryFraudGraphSessionStore()
        self._fail_after = fail_after
        self.appends = 0

    @property
    def events(self) -> dict[str, list[dict]]:
        return self._inner.events

    async def claim_session(self, request):
        return await self._inner.claim_session(request)

    async def append_event(self, session_id, event):
        self.appends += 1
        if self.appends > self._fail_after:
            raise ConnectionError("fraud audit database unavailable")
        await self._inner.append_event(session_id, event)

    async def complete_session(self, session, outcome):
        return await self._inner.complete_session(session, outcome)

    async def mark_published(self, session):
        return await self._inner.mark_published(session)

    async def renew_lease(self, session) -> None:
        return None

    async def release_lease(self, session) -> None:
        return None


def score_request_payload(
    *, payment_id: str = PAYMENT_ID, event_id: str | None = None
) -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "event_id": event_id or f"fraud.score.requested:{payment_id}",
            "payment_id": payment_id,
            "user_id": USER_ID,
            "from_wallet_id": FROM_WALLET_ID,
            "to_wallet_id": TO_WALLET_ID,
            "amount_cents": 45000,
            "currency": "USD",
            "occurred_at": "2026-06-07T00:00:00Z",
            "trace_id": TRACE_ID,
        }
    ).encode()


async def run_fraud_scenario(
    *,
    responses: Sequence[str] | None = None,
    providers: Mapping[str, Sequence[str]] | None = None,
    default_provider: str = PRIMARY_PROVIDER,
    data: object | None = None,
    store: object | None = None,
    payloads: Sequence[bytes] | None = None,
    env_overrides: Mapping[str, str] | None = None,
) -> ScenarioResult:
    """Drive the production worker end to end against a scripted provider."""

    scripts = dict(providers or {PRIMARY_PROVIDER: list(responses or [ALLOW_RESPONSE])})
    async with running_provider_server(scripts) as server:
        environ = {
            **server.registry_env(default_provider),
            "FRAUD_DATABASE_URL": AUDIT_DATABASE_URL,
            **(env_overrides or {}),
        }
        config = FraudConfig.from_env(environ)
        provider_config = load_provider_registry_config(environ)
        provider = next(
            item
            for item in provider_config.providers
            if item.id == provider_config.default_provider_id
        )
        completion = CompletionService(DriverRegistry.from_config(provider_config).default_driver)
        completion.provider_id = provider.id
        completion.model_id = provider.model

        registry = CollectorRegistry()
        metrics = FraudMetrics(registry=registry)
        audit_store = store if store is not None else InMemoryFraudGraphSessionStore()
        service = FraudScoringService(
            data or StubFraudData(), completion, config, store=audit_store, metrics=metrics
        )
        service.provider_id = provider.id
        producer = RecordingProducer()
        publisher = KafkaFraudPublisher(
            producer, provider_id=provider.id, model_id=provider.model, metrics=metrics
        )
        worker = FraudWorker(service, publisher, audit_store, metrics=metrics)

        decisions = [
            await worker.handle_payload(payload)
            for payload in (payloads or [score_request_payload()])
        ]
        return ScenarioResult(
            decisions=decisions,
            published=producer.records,
            store=audit_store,
            server=server,
            metrics_registry=registry,
        )
