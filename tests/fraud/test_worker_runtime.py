import asyncio
import base64
import json
from datetime import UTC, datetime

from app.fraud.dto import FraudOutcome, FraudScoreRequest, FraudVerdict
from app.fraud.integrations.kafka import (
    KafkaDeadLetterPublisher,
    KafkaFraudPublisher,
    KafkaWorkerRunner,
)
from app.fraud.runtime import WorkerHealth, WorkerRuntime, provider_configuration_ready
from app.fraud.worker import ConsumerDecision, FraudWorker, InMemoryFraudSessionStore


def test_kafka_publisher_emits_stable_sanitized_flagged_event() -> None:
    producer = FakeProducer()
    publisher = KafkaFraudPublisher(producer, now=lambda: datetime(2026, 6, 7, tzinfo=UTC))
    request = score_request()
    outcome = FraudOutcome(
        action="flag",
        verdict=FraudVerdict(0.8, "flag", "velocity", "flag", False),
    )

    asyncio.run(publisher.publish_flagged(request, outcome, session_id="session-1"))

    topic, value, key, headers = producer.sent[0]
    event = json.loads(value)
    assert topic == "fraud.flagged"
    assert key == b"payment-1"
    assert event["event_id"] == f"fraud.flagged:{request.event_id}"
    assert event["session_id"] == "session-1"
    assert "private-user" not in value.decode()
    assert "private-wallet" not in value.decode()
    assert dict(headers).get("traceparent") != b"trace-1"


def test_runner_commits_only_commit_decisions_and_stops_polling() -> None:
    consumer = FakeConsumer([FakeRecord(b"bad"), FakeRecord(b"retry")])
    worker = FakeWorker([ConsumerDecision.COMMIT, ConsumerDecision.RETRY])
    runner = KafkaWorkerRunner(consumer, worker)

    asyncio.run(runner.run())

    assert consumer.committed == [consumer.records[0]]
    assert consumer.stopped is True


def test_runner_dead_letters_poison_records_before_committing() -> None:
    consumer = FakeConsumer([FakeRecord(b"not json", offset=11, key=b"payment-1")])
    worker = FakeWorker([ConsumerDecision.DEAD_LETTER])
    producer = FakeProducer()
    runner = KafkaWorkerRunner(
        consumer,
        worker,
        KafkaDeadLetterPublisher(producer, now=lambda: datetime(2026, 6, 7, tzinfo=UTC)),
    )

    asyncio.run(runner.run())

    topic, value, key, _ = producer.sent[0]
    payload = json.loads(value)
    assert topic == "fraud.score.requested.dlq"
    assert key == b"payment-1"
    assert payload["topic"] == "fraud.score.requested"
    assert payload["offset"] == 11
    assert base64.b64decode(payload["value"]) == b"not json"
    assert payload["error"]
    assert consumer.committed == [consumer.records[0]]


def test_runner_holds_offset_when_dead_letter_publish_fails() -> None:
    consumer = FakeConsumer([FakeRecord(b"not json")])
    worker = FakeWorker([ConsumerDecision.DEAD_LETTER])
    runner = KafkaWorkerRunner(consumer, worker, FailingDeadLetters())

    asyncio.run(runner.run())

    assert consumer.committed == []


def test_worker_renews_and_releases_lease_while_scoring() -> None:
    async def run() -> tuple[int, int]:
        store = LeaseStore()
        worker = FraudWorker(
            service=SlowService(),
            publisher=FakePublisher(),
            store=store,
            lease_renew_interval_seconds=0.001,
        )
        assert await worker.handle_payload(valid_payload()) == ConsumerDecision.COMMIT
        return store.renewed, store.released

    renewed, released = asyncio.run(run())
    assert renewed > 0
    assert released == 1


def test_readiness_requires_kafka_schema_database_and_provider() -> None:
    health = WorkerHealth(
        kafka_check=lambda: async_value(True),
        database_check=lambda: async_value(True),
        schema_check=lambda: async_value(False),
        provider_check=lambda: False,
    )

    assert health.liveness()["live"] is True
    readiness = asyncio.run(health.readiness())
    assert readiness == {
        "ready": False,
        "kafka": True,
        "database": True,
        "schema": False,
        "provider": False,
    }


def test_provider_readiness_fails_for_invalid_configuration() -> None:
    assert provider_configuration_ready({}) is False


def test_runtime_shutdown_is_bounded_and_closes_clients() -> None:
    async def run() -> tuple[bool, bool, bool]:
        runner = SlowRunner()
        database = Closable()
        grpc = Closable()
        runtime = WorkerRuntime(runner=runner, database=database, grpc_clients=[grpc])
        task = asyncio.create_task(runtime.run())
        await asyncio.sleep(0)
        await runtime.shutdown(timeout_seconds=0.001)
        await task
        return runner.stopping, database.closed, grpc.closed

    assert asyncio.run(run()) == (True, True, True)


async def async_value(value: bool) -> bool:
    return value


def score_request() -> FraudScoreRequest:
    return FraudScoreRequest(
        schema_version=1,
        event_id="fraud.score.requested:payment-1",
        payment_id="payment-1",
        user_id="private-user",
        from_wallet_id="private-wallet-1",
        to_wallet_id="private-wallet-2",
        amount_cents=100,
        currency="USD",
        occurred_at=datetime(2026, 6, 7, tzinfo=UTC),
        trace_id="trace-1",
    )


def valid_payload() -> bytes:
    request = score_request()
    return json.dumps(
        {
            "schema_version": 1,
            "event_id": request.event_id,
            "payment_id": request.payment_id,
            "user_id": request.user_id,
            "from_wallet_id": request.from_wallet_id,
            "to_wallet_id": request.to_wallet_id,
            "amount_cents": request.amount_cents,
            "currency": request.currency,
            "occurred_at": request.occurred_at.isoformat().replace("+00:00", "Z"),
            "trace_id": request.trace_id,
        }
    ).encode()


class FakeProducer:
    def __init__(self) -> None:
        self.sent = []

    async def send_and_wait(self, topic, *, value, key, headers=None):
        self.sent.append((topic, value, key, headers))


class FakeRecord:
    def __init__(self, value: bytes, topic: str = "fraud.score.requested", partition: int = 0, offset: int = 0, key: bytes | None = None) -> None:
        self.value = value
        self.topic = topic
        self.partition = partition
        self.offset = offset
        self.key = key


class FakeConsumer:
    def __init__(self, records) -> None:
        self.records = records
        self.committed = []
        self.stopped = False

    def __aiter__(self):
        return self._iterate()

    async def _iterate(self):
        for record in self.records:
            yield record

    async def commit_record(self, record) -> None:
        self.committed.append(record)

    async def stop(self) -> None:
        self.stopped = True


class FakeWorker:
    def __init__(self, decisions) -> None:
        self.decisions = list(decisions)

    async def handle_payload(self, payload):
        return self.decisions.pop(0)


class SlowService:
    async def score(self, request):
        await asyncio.sleep(0.005)
        return FraudOutcome(action="allow")


class FakePublisher:
    async def publish_flagged(self, request, outcome, session_id="") -> None:
        pass

    async def publish_error(self, request, reason_code, session_id="") -> None:
        pass


class LeaseStore(InMemoryFraudSessionStore):
    def __init__(self) -> None:
        super().__init__()
        self.renewed = 0
        self.released = 0

    async def renew_lease(self, session) -> None:
        self.renewed += 1

    async def release_lease(self, session) -> None:
        self.released += 1


class SlowRunner:
    def __init__(self) -> None:
        self.stopping = False

    async def run(self) -> None:
        while not self.stopping:
            await asyncio.sleep(1)

    async def shutdown(self) -> None:
        self.stopping = True


class Closable:
    def __init__(self) -> None:
        self.closed = False

    async def close(self) -> None:
        self.closed = True


class FailingDeadLetters:
    async def publish(self, record, reason) -> None:
        raise RuntimeError("broker unavailable")
