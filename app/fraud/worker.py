import json
from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum
from typing import Any
from uuid import uuid4

from app.fraud.dto import FraudOutcome, FraudScoreRequest, FraudSession
from app.fraud.ports import FraudSessionStore


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


class FraudWorker:
    def __init__(
        self,
        service,
        publisher,
        store: FraudSessionStore | None = None,
    ) -> None:
        self._service = service
        self._publisher = publisher
        self._store = store or InMemoryFraudSessionStore()

    async def handle_payload(self, payload: bytes) -> ConsumerDecision:
        parsed = classify_transport_payload(payload)
        if parsed.classification != TransportClassification.VALID or parsed.request is None:
            return ConsumerDecision.COMMIT
        request = parsed.request
        try:
            session = await self._store.claim_session(request)
            outcome = session.outcome
            if not session.completed or outcome is None:
                outcome = await self._service.score(request)
                try:
                    session = await self._store.complete_session(session, outcome)
                except Exception:
                    await self._publisher.publish_error(request, "audit_failed")
                    return ConsumerDecision.RETRY
            if session.output_published:
                return ConsumerDecision.COMMIT
            if outcome.action in ("flag", "block"):
                try:
                    await self._publisher.publish_flagged(request, outcome)
                except Exception:
                    try:
                        await self._publisher.publish_error(request, "publish_failed")
                    finally:
                        return ConsumerDecision.RETRY
                await self._store.mark_published(session)
            elif outcome.reason_code is not None:
                try:
                    await self._publisher.publish_error(request, outcome.reason_code)
                except Exception:
                    pass
                await self._store.mark_published(session)
        except Exception:
            return ConsumerDecision.RETRY
        return ConsumerDecision.COMMIT


class InMemoryFraudSessionStore:
    def __init__(self) -> None:
        self._sessions: dict[str, FraudSession] = {}

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

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        completed = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=True,
            outcome=outcome,
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
            output_published=True,
        )
        self._sessions[session.source_event_id] = published
        return published


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
