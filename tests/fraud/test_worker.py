import asyncio
import json
from datetime import UTC, datetime

from app.fraud.dto import FraudOutcome, FraudVerdict
from app.fraud.worker import (
    ConsumerDecision,
    FraudWorker,
    TransportClassification,
    classify_transport_payload,
)


def valid_payload() -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "event_id": "fraud.score.requested:payment-1",
            "payment_id": "payment-1",
            "user_id": "user-1",
            "from_wallet_id": "wallet-1",
            "to_wallet_id": "wallet-2",
            "amount_cents": 100,
            "currency": "USD",
            "occurred_at": "2026-06-06T00:00:00Z",
            "trace_id": "trace-1",
        }
    ).encode()


def test_transport_classification_rejects_malformed_records_as_non_retryable() -> None:
    assert classify_transport_payload(b"{").classification == TransportClassification.NON_RETRYABLE
    assert (
        classify_transport_payload(
            json.dumps({"schema_version": 2, "event_id": "x"}).encode()
        ).classification
        == TransportClassification.NON_RETRYABLE
    )


def test_worker_publishes_flagged_for_flag_outcome() -> None:
    publisher = FakePublisher()
    worker = FraudWorker(
        service=FakeService(
            FraudOutcome(
                action="flag",
                verdict=FraudVerdict(
                    risk_score=0.8,
                    action="flag",
                    reason="velocity",
                    model_action="flag",
                    action_normalized=False,
                ),
            )
        ),
        publisher=publisher,
    )

    decision = asyncio.run(worker.handle_payload(valid_payload()))

    assert decision == ConsumerDecision.COMMIT
    assert publisher.flagged == ["payment-1"]
    assert publisher.errors == []


def test_worker_publishes_error_for_fail_open_outcome() -> None:
    publisher = FakePublisher()
    worker = FraudWorker(
        service=FakeService(FraudOutcome(action=None, reason_code="validation_failed")),
        publisher=publisher,
    )

    decision = asyncio.run(worker.handle_payload(valid_payload()))

    assert decision == ConsumerDecision.COMMIT
    assert publisher.flagged == []
    assert publisher.errors == [("payment-1", "validation_failed")]


class FakeService:
    def __init__(self, outcome: FraudOutcome) -> None:
        self.outcome = outcome
        self.requests = []

    async def score(self, request):
        self.requests.append(request)
        return self.outcome


class FakePublisher:
    def __init__(self) -> None:
        self.flagged: list[str] = []
        self.errors: list[tuple[str, str]] = []

    async def publish_flagged(self, request, outcome) -> None:
        self.flagged.append(request.payment_id)

    async def publish_error(self, request, reason_code: str) -> None:
        self.errors.append((request.payment_id, reason_code))
