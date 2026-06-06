import asyncio
import json
from datetime import UTC, datetime

from app.fraud.dto import FraudOutcome, FraudVerdict
from app.fraud.worker import (
    ConsumerDecision,
    FraudWorker,
    InMemoryFraudSessionStore,
    SessionClaimError,
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
        store=InMemoryFraudSessionStore(),
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
        store=InMemoryFraudSessionStore(),
    )

    decision = asyncio.run(worker.handle_payload(valid_payload()))

    assert decision == ConsumerDecision.COMMIT
    assert publisher.flagged == []
    assert publisher.errors == [("payment-1", "validation_failed")]


def test_duplicate_completed_request_uses_stored_outcome_without_rescoring() -> None:
    store = InMemoryFraudSessionStore()
    publisher = FakePublisher()
    first_service = FakeService(
        FraudOutcome(
            action="block",
            verdict=FraudVerdict(
                risk_score=0.95,
                action="block",
                reason="velocity",
                model_action="block",
                action_normalized=False,
            ),
        )
    )
    worker = FraudWorker(service=first_service, publisher=publisher, store=store)
    assert asyncio.run(worker.handle_payload(valid_payload())) == ConsumerDecision.COMMIT

    duplicate_service = FakeService(FraudOutcome(action=None, reason_code="should_not_run"))
    duplicate_worker = FraudWorker(
        service=duplicate_service,
        publisher=publisher,
        store=store,
    )

    assert asyncio.run(duplicate_worker.handle_payload(valid_payload())) == ConsumerDecision.COMMIT
    assert len(duplicate_service.requests) == 0
    assert publisher.flagged == ["payment-1"]


def test_duplicate_completed_unpublished_request_republishes_without_rescoring() -> None:
    store = InMemoryFraudSessionStore()
    seed_request = classify_transport_payload(valid_payload()).request
    assert seed_request is not None
    session = asyncio.run(store.claim_session(seed_request))
    asyncio.run(
        store.complete_session(
            session,
            FraudOutcome(
                action="flag",
                verdict=FraudVerdict(
                    risk_score=0.8,
                    action="flag",
                    reason="velocity",
                    model_action="flag",
                    action_normalized=False,
                ),
            ),
        )
    )
    service = FakeService(FraudOutcome(action=None, reason_code="should_not_run"))
    publisher = FakePublisher()

    decision = asyncio.run(
        FraudWorker(service=service, publisher=publisher, store=store).handle_payload(
            valid_payload()
        )
    )

    assert decision == ConsumerDecision.COMMIT
    assert service.requests == []
    assert publisher.flagged == ["payment-1"]


def test_audit_failure_publishes_error_and_leaves_request_retryable() -> None:
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
        store=FailingCompleteStore(),
    )

    decision = asyncio.run(worker.handle_payload(valid_payload()))

    assert decision == ConsumerDecision.RETRY
    assert publisher.errors == [("payment-1", "audit_failed")]
    assert publisher.flagged == []


def test_flagged_publication_failure_attempts_publish_failed_error_and_retries() -> None:
    store = InMemoryFraudSessionStore()
    publisher = FakePublisher(fail_flagged=True)
    worker = FraudWorker(
        service=FakeService(
            FraudOutcome(
                action="block",
                verdict=FraudVerdict(
                    risk_score=0.95,
                    action="block",
                    reason="velocity",
                    model_action="block",
                    action_normalized=False,
                ),
            )
        ),
        publisher=publisher,
        store=store,
    )

    decision = asyncio.run(worker.handle_payload(valid_payload()))

    assert decision == ConsumerDecision.RETRY
    assert publisher.errors == [("payment-1", "publish_failed")]
    session = asyncio.run(store.claim_session(classify_transport_payload(valid_payload()).request))
    assert session.completed is True
    assert session.output_published is False


class FakeService:
    def __init__(self, outcome: FraudOutcome) -> None:
        self.outcome = outcome
        self.requests = []

    async def score(self, request):
        self.requests.append(request)
        return self.outcome


class FakePublisher:
    def __init__(self, fail_flagged: bool = False) -> None:
        self.flagged: list[str] = []
        self.errors: list[tuple[str, str]] = []
        self.fail_flagged = fail_flagged

    async def publish_flagged(self, request, outcome) -> None:
        if self.fail_flagged:
            raise RuntimeError("kafka unavailable")
        self.flagged.append(request.payment_id)

    async def publish_error(self, request, reason_code: str) -> None:
        self.errors.append((request.payment_id, reason_code))


class FailingCompleteStore(InMemoryFraudSessionStore):
    async def complete_session(self, session, outcome):
        raise SessionClaimError("audit unavailable")
