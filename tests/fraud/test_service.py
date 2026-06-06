import asyncio
from datetime import UTC, datetime

from app.fraud.config import FraudConfig
from app.fraud.dto import FraudScoreRequest, KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.service import FraudScoringService


def request() -> FraudScoreRequest:
    return FraudScoreRequest(
        schema_version=1,
        event_id="fraud.score.requested:payment-1",
        payment_id="11111111-1111-1111-1111-111111111111",
        user_id="22222222-2222-2222-2222-222222222222",
        from_wallet_id="33333333-3333-3333-3333-333333333333",
        to_wallet_id="44444444-4444-4444-4444-444444444444",
        amount_cents=1250,
        currency="USD",
        occurred_at=datetime.now(UTC),
        trace_id="trace-1",
    )


def test_service_allows_low_risk_without_raw_ids_in_prompt() -> None:
    data = FakeData()
    completion = FakeCompletion(['{"risk_score":0.2,"action":"allow","reason":"normal pattern"}'])

    outcome = asyncio.run(FraudScoringService(data, completion, FraudConfig()).score(request()))

    assert outcome.action == "allow"
    assert outcome.reason_code is None
    assert completion.calls == 1
    prompt = completion.prompts[0]
    assert request().user_id not in prompt
    assert request().from_wallet_id not in prompt
    assert request().to_wallet_id not in prompt
    assert "sender" in prompt and "recipient" in prompt and "current_payment" in prompt


def test_service_retries_once_after_malformed_response_and_uses_canonical_action() -> None:
    completion = FakeCompletion(
        [
            "{",
            '{"risk_score":0.8,"action":"allow","reason":"velocity above baseline"}',
        ]
    )

    outcome = asyncio.run(FraudScoringService(FakeData(), completion, FraudConfig()).score(request()))

    assert outcome.action == "flag"
    assert outcome.verdict is not None
    assert outcome.verdict.model_action == "allow"
    assert outcome.verdict.action_normalized is True
    assert completion.calls == 2
    assert "invalid_json" in completion.prompts[1]
    assert "{" not in completion.prompts[1]


def test_service_fails_open_after_guard_rejection_without_model_call() -> None:
    config = FraudConfig(prompt_max_chars=20)
    completion = FakeCompletion(['{"risk_score":0.9,"action":"block","reason":"x"}'])

    outcome = asyncio.run(FraudScoringService(FakeData(), completion, config).score(request()))

    assert outcome.action is None
    assert outcome.reason_code == "prompt_too_large"
    assert completion.calls == 0


def test_service_fails_open_when_enrichment_fails() -> None:
    data = FakeData(fail=True)
    outcome = asyncio.run(
        FraudScoringService(data, FakeCompletion([]), FraudConfig()).score(request())
    )

    assert outcome.action is None
    assert outcome.reason_code == "enrichment_failed"


class FakeData:
    def __init__(self, fail: bool = False) -> None:
        self.fail = fail

    async def get_transaction_history(self, wallet_id: str, limit: int, trace_id: str):
        if self.fail:
            raise RuntimeError("ledger unavailable")
        return [
            TransactionHistoryEntry(
                direction="outbound",
                amount_cents=1000,
                currency="USD",
                occurred_at=datetime.now(UTC),
            )
        ]

    async def get_velocity_metrics(self, wallet_id: str, trace_id: str) -> VelocityMetrics:
        if self.fail:
            raise RuntimeError("ledger unavailable")
        return VelocityMetrics(transactions_last_hour=1, amount_last_hour_cents=1000)

    async def get_kyc_status(self, user_id: str, trace_id: str) -> KYCStatus:
        if self.fail:
            raise RuntimeError("verification unavailable")
        return KYCStatus(status="verified")


class FakeCompletion:
    def __init__(self, responses: list[str]) -> None:
        self.responses = responses
        self.calls = 0
        self.prompts: list[str] = []

    async def complete(self, messages) -> str:
        self.calls += 1
        self.prompts.append("\n".join(message.content for message in messages))
        return self.responses.pop(0)
