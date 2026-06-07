import json
from datetime import UTC, datetime
from typing import Any, Callable

from app.fraud.dto import FraudOutcome, FraudScoreRequest
from app.fraud.worker import ConsumerDecision


class KafkaFraudPublisher:
    def __init__(
        self,
        producer: Any,
        *,
        flagged_topic: str = "fraud.flagged",
        error_topic: str = "fraud.error",
        now: Callable[[], datetime] | None = None,
        provider_id: str = "",
        model_id: str = "",
    ) -> None:
        self._producer = producer
        self._flagged_topic = flagged_topic
        self._error_topic = error_topic
        self._now = now or (lambda: datetime.now(UTC))
        self._provider_id = provider_id
        self._model_id = model_id

    async def publish_flagged(
        self, request: FraudScoreRequest, outcome: FraudOutcome, session_id: str = ""
    ) -> None:
        if outcome.verdict is None or outcome.action not in ("flag", "block"):
            raise ValueError("flagged publication requires a flagged verdict")
        verdict = outcome.verdict
        event = {
            "schema_version": 1,
            "event_id": f"fraud.flagged:{request.event_id}",
            "source_event_id": request.event_id,
            "payment_id": request.payment_id,
            "session_id": session_id,
            "action": outcome.action,
            "risk_score": verdict.risk_score,
            "reason": verdict.reason,
            "provider_id": self._provider_id,
            "model_id": self._model_id,
            "occurred_at": _timestamp(self._now()),
            "trace_id": request.trace_id,
        }
        await self._send(
            self._flagged_topic, request.payment_id, event, trace_id=request.trace_id
        )

    async def publish_error(
        self, request: FraudScoreRequest, reason_code: str, session_id: str = ""
    ) -> None:
        event = {
            "schema_version": 1,
            "event_id": f"fraud.error:{request.event_id}:{reason_code}",
            "source_event_id": request.event_id,
            "payment_id": request.payment_id,
            "session_id": session_id,
            "reason_code": reason_code,
            "occurred_at": _timestamp(self._now()),
            "trace_id": request.trace_id,
        }
        await self._send(
            self._error_topic, request.payment_id, event, trace_id=request.trace_id
        )

    async def _send(
        self, topic: str, key: str, event: dict[str, object], *, trace_id: str
    ) -> None:
        headers = [("traceparent", trace_id.encode())] if trace_id else []
        await self._producer.send_and_wait(
            topic,
            value=json.dumps(event, separators=(",", ":")).encode(),
            key=key.encode(),
            headers=headers,
        )


class KafkaWorkerRunner:
    def __init__(self, consumer: Any, worker: Any) -> None:
        self._consumer = consumer
        self._worker = worker
        self._stopping = False

    async def run(self) -> None:
        try:
            async for record in self._consumer:
                if self._stopping:
                    break
                decision = await self._worker.handle_payload(record.value)
                if decision == ConsumerDecision.COMMIT:
                    await self._consumer.commit_record(record)
        finally:
            await self._consumer.stop()

    async def shutdown(self) -> None:
        self._stopping = True


class AIOKafkaConsumerAdapter:
    def __init__(self, consumer: Any) -> None:
        self._consumer = consumer

    def __aiter__(self):
        return self._consumer.__aiter__()

    async def commit_record(self, record: Any) -> None:
        from aiokafka import TopicPartition
        from aiokafka.structs import OffsetAndMetadata

        partition = TopicPartition(record.topic, record.partition)
        await self._consumer.commit(
            {partition: OffsetAndMetadata(record.offset + 1, "")}
        )

    async def connectivity_ready(self) -> bool:
        try:
            return bool(await self._consumer.topics())
        except Exception:
            return False

    async def stop(self) -> None:
        await self._consumer.stop()


def _timestamp(value: datetime) -> str:
    return value.astimezone(UTC).isoformat().replace("+00:00", "Z")
