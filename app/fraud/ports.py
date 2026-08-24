from collections.abc import Sequence
from typing import Protocol

from app.fraud.dto import (
    FraudOutcome,
    FraudScoreRequest,
    FraudSession,
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
    async def publish_flagged(
        self, request: FraudScoreRequest, outcome: object, session_id: str = ""
    ) -> None: ...

    async def publish_error(
        self, request: FraudScoreRequest, reason_code: str, session_id: str = ""
    ) -> None: ...


class FraudSessionStore(Protocol):
    async def claim_session(self, request: FraudScoreRequest) -> FraudSession: ...

    async def append_event(self, session_id: str, event: dict[str, object]) -> None: ...

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession: ...

    async def mark_published(self, session: FraudSession) -> FraudSession: ...

    async def renew_lease(self, session: FraudSession) -> None: ...

    async def release_lease(self, session: FraudSession) -> None: ...
