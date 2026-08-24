import asyncio
import json
from datetime import UTC, datetime

from prometheus_client import CollectorRegistry, generate_latest

from app.fraud.config import FraudConfig
from app.fraud.dto import KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.service import FraudScoringService
from app.fraud.worker import ConsumerDecision, FraudWorker, InMemoryFraudSessionStore


def test_worker_integration_uses_fake_model_and_enrichment_and_deduplicates() -> None:
    store = InMemoryFraudSessionStore()
    completion = FakeCompletion()
    data = FakeData()
    publisher = FakePublisher()
    service = FraudScoringService(data, completion, FraudConfig(), store=store)
    worker = FraudWorker(service, publisher, store)

    assert asyncio.run(worker.handle_payload(payload())) == ConsumerDecision.COMMIT
    assert asyncio.run(worker.handle_payload(payload())) == ConsumerDecision.COMMIT

    assert completion.calls == 1
    assert data.calls == 3
    assert publisher.flagged == ["fraud.flagged:fraud.score.requested:payment-1"]


def test_local_scoring_scenario_emits_fraud_dashboard_metrics() -> None:
    registry = CollectorRegistry()
    from app.fraud.metrics import FraudMetrics

    metrics = FraudMetrics(registry=registry)
    store = InMemoryFraudSessionStore()
    completion = FakeCompletion()
    data = FakeData()
    service = FraudScoringService(data, completion, FraudConfig(), store=store, metrics=metrics)
    service.provider_id = completion.provider_id
    worker = FraudWorker(service, FakePublisher(), store, metrics=metrics)

    assert asyncio.run(worker.handle_payload(payload())) == ConsumerDecision.COMMIT

    rendered = generate_latest(registry).decode()
    assert 'fraud_transactions_scored_total{action="flag",provider="fake"} 1.0' in rendered
    assert "fraud_risk_score_bucket" in rendered
    assert 'fraud_enrichment_calls_total{method="history",outcome="success"} 1.0' in rendered
    assert 'fraud_model_latency_seconds_count{model="fake-model",provider="fake"} 1.0' in rendered


def payload() -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "event_id": "fraud.score.requested:payment-1",
            "payment_id": "payment-1",
            "user_id": "private-user",
            "from_wallet_id": "private-wallet-1",
            "to_wallet_id": "private-wallet-2",
            "amount_cents": 5000,
            "currency": "USD",
            "occurred_at": "2026-06-07T00:00:00Z",
            "trace_id": "trace-1",
        }
    ).encode()


class FakeData:
    def __init__(self) -> None:
        self.calls = 0

    async def get_transaction_history(self, wallet_id, limit, trace_id):
        self.calls += 1
        return [TransactionHistoryEntry("outbound", 100, "USD", datetime.now(UTC))]

    async def get_velocity_metrics(self, wallet_id, trace_id):
        self.calls += 1
        return VelocityMetrics(transactions_last_hour=10)

    async def get_kyc_status(self, user_id, trace_id):
        self.calls += 1
        return KYCStatus("verified")


class FakeCompletion:
    provider_id = "fake"
    model_id = "fake-model"

    def __init__(self) -> None:
        self.calls = 0

    async def complete(self, messages):
        self.calls += 1
        return '{"risk_score":0.8,"action":"flag","reason":"velocity"}'


class FakePublisher:
    def __init__(self) -> None:
        self.flagged = []

    async def publish_flagged(self, request, outcome, session_id=""):
        self.flagged.append(f"fraud.flagged:{request.event_id}")

    async def publish_error(self, request, reason_code, session_id=""):
        raise AssertionError(reason_code)
