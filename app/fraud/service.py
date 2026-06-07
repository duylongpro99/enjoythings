import asyncio

from app.fraud.config import FraudConfig
from app.fraud.dto import (
    FraudOutcome,
    FraudScoreRequest,
    SanitizedTransactionFacts,
    TransactionHistoryEntry,
)
from app.fraud.guards import guard_prompt
from app.fraud.instruction import SYSTEM_INSTRUCTION
from app.fraud.ports import CompletionPort, FraudDataPort
from app.fraud.validator import validate_verdict
from app.llm.types import ChatMessage


class FraudScoringService:
    def __init__(
        self,
        data: FraudDataPort,
        completion: CompletionPort,
        config: FraudConfig,
    ) -> None:
        self._data = data
        self._completion = completion
        self._config = config

    async def score(self, request: FraudScoreRequest) -> FraudOutcome:
        try:
            facts = await self._enrich(request)
        except Exception:
            return FraudOutcome(action=None, reason_code="enrichment_failed")

        prompt = _build_prompt(facts)
        messages = _messages(prompt)
        rejection = self._guard_messages(messages)
        if rejection is not None:
            return FraudOutcome(action=None, reason_code="prompt_rejected")

        try:
            first = await self._complete_and_validate(messages)
        except Exception:
            return FraudOutcome(action=None, reason_code="model_failed")
        if first.verdict is not None:
            return FraudOutcome(action=first.verdict.action, verdict=first.verdict)

        retry_prompt = (
            f"{first.corrective_prompt}\n"
            f"Sanitized facts: {_facts_sentence(facts)}"
        )
        retry_messages = _messages(retry_prompt)
        rejection = self._guard_messages(retry_messages)
        if rejection is not None:
            return FraudOutcome(action=None, reason_code="prompt_rejected")
        try:
            second = await self._complete_and_validate(retry_messages)
        except Exception:
            return FraudOutcome(action=None, reason_code="model_failed")
        if second.verdict is not None:
            return FraudOutcome(action=second.verdict.action, verdict=second.verdict)
        return FraudOutcome(action=None, reason_code="validation_failed")

    async def _enrich(self, request: FraudScoreRequest) -> SanitizedTransactionFacts:
        history, velocity, kyc = await asyncio.gather(
            self._data.get_transaction_history(
                request.from_wallet_id, self._config.history_limit, request.trace_id
            ),
            self._data.get_velocity_metrics(request.from_wallet_id, request.trace_id),
            self._data.get_kyc_status(request.user_id, request.trace_id),
        )
        ordered_history = tuple(
            sorted(history, key=lambda entry: entry.occurred_at, reverse=True)
        )
        return SanitizedTransactionFacts(
            amount_cents=request.amount_cents,
            currency=request.currency,
            sender_kyc_status=kyc.status,
            history=ordered_history,
            velocity=velocity,
        )

    def _guard_messages(self, messages: list[ChatMessage]) -> str | None:
        return guard_prompt(
            "\n".join(message.content for message in messages),
            max_chars=self._config.prompt_max_chars,
            sensitive_keys=self._config.sensitive_keys,
        )

    async def _complete_and_validate(self, messages: list[ChatMessage]):
        response = await self._completion.complete(messages)
        return validate_verdict(
            response,
            score_threshold=self._config.score_threshold,
            block_threshold=self._config.block_threshold,
            max_chars=self._config.response_max_chars,
            sensitive_keys=self._config.sensitive_keys,
        )


def _messages(prompt: str) -> list[ChatMessage]:
    return [
        ChatMessage(role="system", content=SYSTEM_INSTRUCTION),
        ChatMessage(role="user", content=prompt),
    ]


def _build_prompt(facts: SanitizedTransactionFacts) -> str:
    return (
        "current_payment amount "
        f"{facts.amount_cents} {facts.currency}; sender kyc {facts.sender_kyc_status}; "
        "recipient semantic label recipient; "
        f"{_facts_sentence(facts)}"
    )


def _facts_sentence(facts: SanitizedTransactionFacts) -> str:
    history = ", ".join(_history_sentence(entry) for entry in facts.history) or "none"
    return (
        f"velocity last hour count {facts.velocity.transactions_last_hour}, "
        f"velocity last hour cents {facts.velocity.amount_last_hour_cents}, "
        f"average 30d cents {facts.velocity.average_amount_30d_cents}, "
        f"distinct recipients 30d {facts.velocity.distinct_recipients_30d}, "
        f"recent history {history}"
    )


def _history_sentence(entry: TransactionHistoryEntry) -> str:
    return (
        f"{entry.direction} {entry.amount_cents} {entry.currency} "
        f"at {entry.occurred_at.isoformat()}"
    )
