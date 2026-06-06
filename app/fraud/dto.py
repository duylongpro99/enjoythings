from dataclasses import dataclass, field
from datetime import datetime
from typing import Literal

FraudAction = Literal["allow", "flag", "block"]


@dataclass(frozen=True)
class FraudScoreRequest:
    schema_version: int
    event_id: str
    payment_id: str
    user_id: str
    from_wallet_id: str
    to_wallet_id: str
    amount_cents: int
    currency: str
    occurred_at: datetime
    trace_id: str = ""


@dataclass(frozen=True)
class TransactionHistoryEntry:
    direction: str
    amount_cents: int
    currency: str
    occurred_at: datetime


@dataclass(frozen=True)
class VelocityMetrics:
    transactions_last_hour: int = 0
    amount_last_hour_cents: int = 0
    average_amount_30d_cents: int = 0
    distinct_recipients_30d: int = 0


@dataclass(frozen=True)
class KYCStatus:
    status: str


@dataclass(frozen=True)
class SanitizedTransactionFacts:
    amount_cents: int
    currency: str
    sender_kyc_status: str
    history: tuple[TransactionHistoryEntry, ...] = ()
    velocity: VelocityMetrics = field(default_factory=VelocityMetrics)


@dataclass(frozen=True)
class FraudVerdict:
    risk_score: float
    action: FraudAction
    reason: str
    model_action: FraudAction
    action_normalized: bool


@dataclass(frozen=True)
class FraudOutcome:
    action: FraudAction | None
    verdict: FraudVerdict | None = None
    reason_code: str | None = None
