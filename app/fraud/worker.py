import json
from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import Any

from app.fraud.dto import FraudOutcome, FraudScoreRequest


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


class FraudWorker:
    def __init__(self, service, publisher) -> None:
        self._service = service
        self._publisher = publisher

    async def handle_payload(self, payload: bytes) -> ConsumerDecision:
        parsed = classify_transport_payload(payload)
        if parsed.classification != TransportClassification.VALID or parsed.request is None:
            return ConsumerDecision.COMMIT
        try:
            outcome: FraudOutcome = await self._service.score(parsed.request)
            if outcome.action in ("flag", "block"):
                await self._publisher.publish_flagged(parsed.request, outcome)
            elif outcome.reason_code is not None:
                await self._publisher.publish_error(parsed.request, outcome.reason_code)
        except Exception:
            return ConsumerDecision.RETRY
        return ConsumerDecision.COMMIT


def classify_transport_payload(payload: bytes) -> TransportResult:
    try:
        raw = json.loads(payload)
    except json.JSONDecodeError:
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if not isinstance(raw, dict) or raw.get("schema_version") != 1:
        return TransportResult(TransportClassification.NON_RETRYABLE)
    required = (
        "event_id",
        "payment_id",
        "user_id",
        "from_wallet_id",
        "to_wallet_id",
        "amount_cents",
        "currency",
        "occurred_at",
    )
    if any(not raw.get(field) for field in required):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    try:
        amount = int(raw["amount_cents"])
        occurred_at = _parse_time(raw["occurred_at"])
    except (TypeError, ValueError):
        return TransportResult(TransportClassification.NON_RETRYABLE)
    if amount <= 0:
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
            amount_cents=amount,
            currency=str(raw["currency"]),
            occurred_at=occurred_at,
            trace_id=str(raw.get("trace_id", "")),
        ),
    )


def _parse_time(value: Any) -> datetime:
    if not isinstance(value, str):
        raise ValueError("timestamp must be a string")
    return datetime.fromisoformat(value.replace("Z", "+00:00"))
