from app.fraud.config import FraudConfig
from app.fraud.dto import (
    FraudOutcome,
    FraudScoreRequest,
    SanitizedTransactionFacts,
    TransactionHistoryEntry,
)
from app.fraud.graph import (
    FraudScoringGraph,
    InMemoryFraudGraphSessionStore,
    build_prompt,
    facts_sentence,
    messages,
)
from app.fraud.instruction import SYSTEM_INSTRUCTION
from app.fraud.metrics import DEFAULT_METRICS, FraudMetrics
from app.fraud.ports import CompletionPort, FraudDataPort, FraudSessionStore

UNKNOWN_PROVIDER = "unknown"


class FraudScoringService:
    def __init__(
        self,
        data: FraudDataPort,
        completion: CompletionPort,
        config: FraudConfig,
        store: FraudSessionStore | None = None,
        metrics: FraudMetrics = DEFAULT_METRICS,
        provider_id: str = UNKNOWN_PROVIDER,
    ) -> None:
        # The metric label is a bounded value, so an unconfigured provider is
        # reported as "unknown" rather than as an empty label.
        self.provider_id = provider_id or UNKNOWN_PROVIDER
        self._graph = FraudScoringGraph(
            data=data,
            completion=completion,
            store=store or InMemoryFraudGraphSessionStore(),
            config=config,
            system_instruction=SYSTEM_INSTRUCTION,
            metrics=metrics,
        )

    async def score(self, request: FraudScoreRequest) -> FraudOutcome:
        return await self._graph.score(request)


def _messages(prompt: str):
    return messages(prompt, system_instruction=SYSTEM_INSTRUCTION)


def _build_prompt(facts: SanitizedTransactionFacts) -> str:
    return build_prompt(facts)


def _facts_sentence(facts: SanitizedTransactionFacts) -> str:
    return facts_sentence(facts)


def _history_sentence(entry: TransactionHistoryEntry) -> str:
    return (
        f"{entry.direction} {entry.amount_cents} {entry.currency} "
        f"at {entry.occurred_at.isoformat()}"
    )
