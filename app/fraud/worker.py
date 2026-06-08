import asyncio
import json
import logging
from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum
from time import monotonic
from typing import Any
from uuid import uuid4

from app.fraud.dto import FraudOutcome, FraudScoreRequest, FraudSession
from app.fraud.ports import FraudSessionStore
from app.fraud.metrics import DEFAULT_METRICS, FraudMetrics
from app.fraud.tracing import extract_kafka_context, start_span


class TransportClassification(StrEnum):
    VALID = "valid"
    NON_RETRYABLE = "non_retryable"


class ConsumerDecision(StrEnum):
    COMMIT = "commit"
    RETRY = "retry"


@dataclass(frozen=True)
class TransportResult:
    classification: TransportClassification
    request: FraudScoreRequest | None = None


class SessionClaimError(RuntimeError):
    pass


class FraudWorkerTelemetry:
    def __init__(self) -> None:
        self.malformed_records = 0
        self.error_publish_failures = 0
        self._logger = logging.getLogger("app.fraud.worker")

    def malformed_record(self) -> None:
        self.malformed_records += 1
        self._logger.warning("fraud worker committed malformed transport event")

    def error_publish_failed(self, request: FraudScoreRequest, reason_code: str) -> None:
        self.error_publish_failures += 1
        self._logger.warning(
            "fraud error publication failed",
            extra={"source_event_id": request.event_id, "reason_code": reason_code},
        )


class FraudWorker:
    def __init__(
        self,
        service,
        publisher,
        store: FraudSessionStore | None = None,
        lease_renew_interval_seconds: float = 20.0,
        telemetry: FraudWorkerTelemetry | None = None,
        metrics: FraudMetrics = DEFAULT_METRICS,
    ) -> None:
        self._service = service
        self._publisher = publisher
        self._store = store or InMemoryFraudSessionStore()
        self._lease_renew_interval_seconds = lease_renew_interval_seconds
        self._telemetry = telemetry or FraudWorkerTelemetry()
        self._metrics = metrics

    async def handle_payload(self, payload: bytes, headers=None) -> ConsumerDecision:
        context = extract_kafka_context(headers)
        with start_span("fraud.worker.consume", context=context, operation="consume"):
            parsed = classify_transport_payload(payload)
            if parsed.classification != TransportClassification.VALID or parsed.request is None:
                self._telemetry.malformed_record()
                return ConsumerDecision.COMMIT
            request = parsed.request
            return await self._handle_request(request)

    async def _handle_request(self, request: FraudScoreRequest) -> ConsumerDecision:
        started = monotonic()
        session: FraudSession | None = None
        heartbeat: asyncio.Task[None] | None = None
        try:
            session = await self._store.claim_session(request)
            outcome = session.outcome
            if not session.completed or outcome is None:
                heartbeat = asyncio.create_task(self._renew_lease(session))
                outcome = await self._service.score(request)
                action = outcome.action or "fail_open"
                self._metrics.transaction_scored(
                    action, getattr(self._service, "provider_id", "unknown")
                )
                await _cancel(heartbeat)
                heartbeat = None
                if outcome.reason_code == "audit_failed":
                    try:
                        await self._publisher.publish_error(
                            request, "audit_failed", session.session_id
                        )
                    except Exception:
                        self._telemetry.error_publish_failed(request, "audit_failed")
                        pass
                    return ConsumerDecision.RETRY
                try:
                    session = await self._store.complete_session(session, outcome)
                except Exception:
                    try:
                        await self._publisher.publish_error(
                            request, "audit_failed", session.session_id
                        )
                    except Exception:
                        self._telemetry.error_publish_failed(request, "audit_failed")
                    return ConsumerDecision.RETRY
            if session.output_published:
                return ConsumerDecision.COMMIT
            if outcome.action in ("flag", "block"):
                try:
                    await self._publisher.publish_flagged(
                        request, outcome, session.session_id
                    )
                except Exception:
                    try:
                        await self._publisher.publish_error(
                            request, "publish_failed", session.session_id
                        )
                    except Exception:
                        self._telemetry.error_publish_failed(request, "publish_failed")
                    finally:
                        return ConsumerDecision.RETRY
                await self._store.mark_published(session)
            elif outcome.reason_code is not None:
                try:
                    await self._publisher.publish_error(
                        request, outcome.reason_code, session.session_id
                    )
                except Exception:
                    self._telemetry.error_publish_failed(request, outcome.reason_code)
                    pass
                await self._store.mark_published(session)
        except Exception:
            return ConsumerDecision.RETRY
        finally:
            outcome_label = "failure"
            if session is not None and session.outcome is not None:
                outcome_label = session.outcome.action or "fail_open"
            self._metrics.session_duration(outcome_label, monotonic() - started)
            await _cancel(heartbeat)
            if session is not None:
                try:
                    await self._store.release_lease(session)
                except Exception:
                    pass
        return ConsumerDecision.COMMIT

    async def _renew_lease(self, session: FraudSession) -> None:
        while True:
            await asyncio.sleep(self._lease_renew_interval_seconds)
            await self._store.renew_lease(session)


class InMemoryFraudSessionStore:
    def __init__(self) -> None:
        self._sessions: dict[str, FraudSession] = {}
        self._events: dict[str, list[dict[str, object]]] = {}

    async def claim_session(self, request: FraudScoreRequest) -> FraudSession:
        existing = self._sessions.get(request.event_id)
        if existing is not None:
            return existing
        session = FraudSession(
            session_id=str(uuid4()),
            source_event_id=request.event_id,
            payment_id=request.payment_id,
        )
        self._sessions[request.event_id] = session
        return session

    async def append_event(self, session_id: str, event: dict[str, object]) -> None:
        self._events.setdefault(session_id, []).append(dict(event))

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        completed = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=True,
            outcome=outcome,
            output_event_type=_output_event_type(outcome),
            output_published=session.output_published,
        )
        self._sessions[session.source_event_id] = completed
        return completed

    async def mark_published(self, session: FraudSession) -> FraudSession:
        published = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=session.completed,
            outcome=session.outcome,
            output_event_type=session.output_event_type,
            output_published=True,
        )
        self._sessions[session.source_event_id] = published
        return published

    async def renew_lease(self, session: FraudSession) -> None:
        return None

    async def release_lease(self, session: FraudSession) -> None:
        return None


def classify_transport_payload(payload: bytes) -> TransportResult:
    try:
        raw = json.loads(payload)
    except json.JSONDecodeError:
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if not isinstance(raw, dict) or raw.get("schema_version") != 1:
        return TransportResult(TransportClassification.NON_RETRYABLE)
    required_strings = (
        "event_id",
        "payment_id",
        "user_id",
        "from_wallet_id",
        "to_wallet_id",
        "currency",
        "occurred_at",
        "trace_id",
    )
    if any(
        not isinstance(raw.get(field), str) or not raw[field]
        for field in required_strings
    ):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if (
        not isinstance(raw.get("amount_cents"), int)
        or isinstance(raw["amount_cents"], bool)
        or raw["amount_cents"] <= 0
    ):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    try:
        occurred_at = _parse_time(raw["occurred_at"])
    except (TypeError, ValueError):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if raw["event_id"] != f"fraud.score.requested:{raw['payment_id']}":
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if occurred_at.utcoffset() != timedelta(0):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    return TransportResult(
        classification=TransportClassification.VALID,
        request=FraudScoreRequest(
            schema_version=1,
            event_id=str(raw["event_id"]),
            payment_id=str(raw["payment_id"]),
            user_id=str(raw["user_id"]),
            from_wallet_id=str(raw["from_wallet_id"]),
            to_wallet_id=str(raw["to_wallet_id"]),
            amount_cents=raw["amount_cents"],
            currency=str(raw["currency"]),
            occurred_at=occurred_at,
            trace_id=str(raw["trace_id"]),
        ),
    )


def _parse_time(value: Any) -> datetime:
    if not isinstance(value, str):
        raise ValueError("timestamp must be a string")
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


async def _cancel(task: asyncio.Task[None] | None) -> None:
    if task is None:
        return
    task.cancel()
    try:
        await task
    except asyncio.CancelledError:
        pass


def _output_event_type(outcome: FraudOutcome) -> str:
    if outcome.action in ("flag", "block"):
        return "fraud.flagged"
    if outcome.reason_code:
        return "fraud.error"
    return ""
