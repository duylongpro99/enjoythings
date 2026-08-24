import base64
import inspect
import json
import logging
from collections.abc import Callable
from datetime import UTC, datetime
from typing import Any

from app.fraud.dto import FraudOutcome, FraudScoreRequest
from app.fraud.metrics import DEFAULT_METRICS, FraudMetrics
from app.fraud.tracing import inject_kafka_context, start_span
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
        metrics: FraudMetrics = DEFAULT_METRICS,
    ) -> None:
        self._producer = producer
        self._flagged_topic = flagged_topic
        self._error_topic = error_topic
        self._now = now or (lambda: datetime.now(UTC))
        self._provider_id = provider_id
        self._model_id = model_id
        self._metrics = metrics

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
        with start_span("fraud.kafka.publish", topic=topic, operation="produce", payment_id=str(event.get("payment_id", ""))):
            headers = inject_kafka_context([])
            try:
                await self._producer.send_and_wait(
                    topic,
                    value=json.dumps(event, separators=(",", ":")).encode(),
                    key=key.encode(),
                    headers=headers,
                )
            except Exception:
                self._metrics.event_published(topic, "failure")
                raise
            self._metrics.event_published(topic, "success")


DEAD_LETTER_SUFFIX = ".dlq"
DEAD_LETTER_SCHEMA_VERSION = 1


class KafkaDeadLetterPublisher:
    """Parks unparseable records on a per-topic dead-letter topic.

    The payload matches the Go services byte for byte, so one dead-letter
    consumer can read every topic: raw key and value stay base64 encoded
    because a poison record is not necessarily valid JSON, or even UTF-8.
    """

    def __init__(self, producer: Any, now: Callable[[], datetime] | None = None) -> None:
        self._producer = producer
        self._now = now or (lambda: datetime.now(UTC))

    async def publish(self, record: Any, reason: str) -> None:
        topic = getattr(record, "topic", "") or ""
        key = getattr(record, "key", None)
        value = getattr(record, "value", None) or b""
        payload = {
            "schema_version": DEAD_LETTER_SCHEMA_VERSION,
            "topic": topic,
            "partition": getattr(record, "partition", 0),
            "offset": getattr(record, "offset", 0),
            "value": base64.b64encode(value).decode(),
            "error": reason,
            "failed_at": _timestamp(self._now()),
        }
        if key:
            payload["key"] = base64.b64encode(key).decode()
        await self._producer.send_and_wait(
            topic + DEAD_LETTER_SUFFIX,
            value=json.dumps(payload, separators=(",", ":")).encode(),
            key=key,
        )


class KafkaWorkerRunner:
    def __init__(self, consumer: Any, worker: Any, dead_letters: Any = None) -> None:
        self._consumer = consumer
        self._worker = worker
        self._dead_letters = dead_letters
        self._stopping = False
        self._logger = logging.getLogger("app.fraud.kafka")

    async def run(self) -> None:
        try:
            async for record in self._consumer:
                if self._stopping:
                    break
                decision = await self._handle_record(record)
                if decision == ConsumerDecision.DEAD_LETTER:
                    if not await self._dead_letter(record):
                        continue
                    decision = ConsumerDecision.COMMIT
                if decision == ConsumerDecision.COMMIT:
                    await self._consumer.commit_record(record)
        finally:
            await self._consumer.stop()

    async def _dead_letter(self, record: Any) -> bool:
        """Park a poison record. Returns False to hold the offset for a retry."""
        if self._dead_letters is None:
            self._logger.warning(
                "fraud worker dropped malformed record without a dead-letter publisher",
                extra={"topic": getattr(record, "topic", ""), "offset": getattr(record, "offset", 0)},
            )
            return True
        try:
            await self._dead_letters.publish(record, "transport payload is not a valid fraud scoring request")
        except Exception:
            self._logger.warning(
                "fraud worker dead-letter publish failed",
                extra={"topic": getattr(record, "topic", ""), "offset": getattr(record, "offset", 0)},
            )
            return False
        return True

    async def shutdown(self) -> None:
        self._stopping = True

    async def _handle_record(self, record: Any) -> ConsumerDecision:
        handle_payload = self._worker.handle_payload
        if "headers" in inspect.signature(handle_payload).parameters:
            return await handle_payload(record.value, headers=getattr(record, "headers", None))
        return await handle_payload(record.value)


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
