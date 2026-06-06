from collections.abc import Sequence
from typing import Protocol

from app.fraud.dto import (
    FraudScoreRequest,
    KYCStatus,
    TransactionHistoryEntry,
    VelocityMetrics,
)
from app.llm.types import ChatMessage


class FraudDataPort(Protocol):
    async def get_transaction_history(
        self, wallet_id: str, limit: int, trace_id: str
    ) -> Sequence[TransactionHistoryEntry]: ...

    async def get_velocity_metrics(
        self, wallet_id: str, trace_id: str
    ) -> VelocityMetrics: ...

    async def get_kyc_status(self, user_id: str, trace_id: str) -> KYCStatus: ...


class CompletionPort(Protocol):
    async def complete(self, messages: list[ChatMessage]) -> str: ...


class FraudPublisher(Protocol):
    async def publish_flagged(self, request: FraudScoreRequest, outcome: object) -> None: ...

    async def publish_error(self, request: FraudScoreRequest, reason_code: str) -> None: ...
