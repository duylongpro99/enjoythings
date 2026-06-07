import asyncio
from datetime import UTC, datetime, timedelta

from app.fraud.config import FraudConfig
from app.fraud.dto import (
    FraudOutcome,
    FraudScoreRequest,
    FraudSession,
    KYCStatus,
    TransactionHistoryEntry,
    VelocityMetrics,
)
from app.fraud.graph import FraudScoringGraph


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
        occurred_at=datetime(2026, 6, 6, tzinfo=UTC),
        trace_id="trace-1",
    )


def test_graph_persists_session_and_audit_events_before_returning_flagged_outcome() -> None:
    store = FakeStore()
    completion = FakeCompletion(
        ['{"risk_score":0.95,"action":"allow","reason":"critical velocity"}'],
        provider_id="fake-provider",
        model_id="fake-model",
    )

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(),
            completion=completion,
            store=store,
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome.action == "block"
    assert store.completed_outcomes == [outcome]
    assert store.claimed_event_ids == [request().event_id]
    assert [event["node"] for event in store.events] == [
        "create_session",
        "build_sanitized_context",
        "enrich_transaction",
        "build_prompt",
        "input_guard",
        "call_llm",
        "validate_verdict",
        "complete_session",
    ]
    llm_event = next(event for event in store.events if event["node"] == "call_llm")
    assert llm_event["provider_id"] == "fake-provider"
    assert llm_event["model_id"] == "fake-model"
    assert llm_event["latency_ms"] == 1000
    assert llm_event["raw_response"] == '{"risk_score":0.95,"action":"allow","reason":"critical velocity"}'
    validation_event = next(
        event for event in store.events if event["node"] == "validate_verdict"
    )
    assert validation_event["action_normalized"] is True
    assert completion.calls == 1


def test_graph_retries_once_without_embedding_rejected_raw_model_response() -> None:
    store = FakeStore()
    completion = FakeCompletion(
        [
            '{"risk_score":',
            '{"risk_score":0.8,"action":"allow","reason":"velocity above baseline"}',
        ]
    )

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(),
            completion=completion,
            store=store,
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome.action == "flag"
    assert completion.calls == 2
    assert '{"risk_score":' not in completion.prompts[1]
    assert "invalid_json" in completion.prompts[1]
    assert request().user_id not in completion.prompts[1]
    assert request().from_wallet_id not in completion.prompts[1]
    assert request().to_wallet_id not in completion.prompts[1]
    assert [
        event["rejection_code"]
        for event in store.events
        if event["node"] == "validate_verdict" and event["outcome"] == "rejected"
    ] == ["invalid_json"]


def test_graph_guard_rejection_records_before_code_and_skips_model_call() -> None:
    store = FakeStore()
    completion = FakeCompletion(
        ['{"risk_score":0.8,"action":"flag","reason":"velocity"}']
    )

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(kyc_status="contains user_id"),
            completion=completion,
            store=store,
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome == FraudOutcome(action=None, reason_code="prompt_rejected")
    assert completion.calls == 0
    assert any(
        event["node"] == "input_guard"
        and event["outcome"] == "rejected"
        and event["rejection_code"] == "sensitive_key_detected"
        and event["rejection_phase"] == "before"
        for event in store.events
    )
    assert store.completed_outcomes == [outcome]


def test_graph_enrichment_failure_fails_open_without_partial_model_prompt() -> None:
    store = FakeStore()
    completion = FakeCompletion(
        ['{"risk_score":0.9,"action":"block","reason":"velocity"}']
    )

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(fail=True),
            completion=completion,
            store=store,
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome == FraudOutcome(action=None, reason_code="enrichment_failed")
    assert completion.calls == 0
    assert store.completed_outcomes == [outcome]


def test_graph_completion_persistence_failure_returns_contract_audit_reason() -> None:
    completion = FakeCompletion(
        ['{"risk_score":0.95,"action":"block","reason":"critical velocity"}']
    )

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(),
            completion=completion,
            store=FailingCompleteStore(),
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome == FraudOutcome(action=None, reason_code="audit_failed")


def test_graph_final_audit_event_failure_does_not_mark_flagged_outcome_complete() -> None:
    store = FailingFinalEventStore()

    outcome = asyncio.run(
        FraudScoringGraph(
            data=FakeData(),
            completion=FakeCompletion(
                ['{"risk_score":0.95,"action":"block","reason":"critical velocity"}']
            ),
            store=store,
            config=FraudConfig(),
            clock=FixedClock(),
        ).score(request())
    )

    assert outcome == FraudOutcome(action=None, reason_code="audit_failed")
    assert store.completed_outcomes == []


class FakeData:
    def __init__(self, *, fail: bool = False, kyc_status: str = "verified") -> None:
        self.fail = fail
        self.kyc_status = kyc_status

    async def get_transaction_history(self, wallet_id: str, limit: int, trace_id: str):
        if self.fail:
            raise RuntimeError("ledger unavailable")
        return [
            TransactionHistoryEntry(
                direction="outbound",
                amount_cents=1000,
                currency="USD",
                occurred_at=datetime(2026, 6, 6, 0, 0, tzinfo=UTC),
            ),
            TransactionHistoryEntry(
                direction="inbound",
                amount_cents=500,
                currency="USD",
                occurred_at=datetime(2026, 6, 5, 0, 0, tzinfo=UTC),
            ),
        ]

    async def get_velocity_metrics(self, wallet_id: str, trace_id: str) -> VelocityMetrics:
        if self.fail:
            raise RuntimeError("ledger unavailable")
        return VelocityMetrics(
            transactions_last_hour=4,
            amount_last_hour_cents=4000,
            average_amount_30d_cents=700,
            distinct_recipients_30d=3,
        )

    async def get_kyc_status(self, user_id: str, trace_id: str) -> KYCStatus:
        if self.fail:
            raise RuntimeError("verification unavailable")
        return KYCStatus(status=self.kyc_status)


class FakeCompletion:
    def __init__(
        self,
        responses: list[str],
        *,
        provider_id: str = "provider",
        model_id: str = "model",
    ) -> None:
        self.responses = responses
        self.provider_id = provider_id
        self.model_id = model_id
        self.calls = 0
        self.prompts: list[str] = []

    async def complete(self, messages):
        self.calls += 1
        self.prompts.append("\n".join(message.content for message in messages))
        return self.responses.pop(0)


class FakeStore:
    def __init__(self) -> None:
        self.session = FraudSession(
            session_id="session-1",
            source_event_id=request().event_id,
            payment_id=request().payment_id,
        )
        self.claimed_event_ids: list[str] = []
        self.events: list[dict[str, object]] = []
        self.completed_outcomes: list[FraudOutcome] = []

    async def claim_session(self, req: FraudScoreRequest) -> FraudSession:
        self.claimed_event_ids.append(req.event_id)
        return self.session

    async def append_event(self, session_id: str, event: dict[str, object]) -> None:
        assert session_id == self.session.session_id
        self.events.append(dict(event))

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        assert session == self.session
        self.completed_outcomes.append(outcome)
        self.session = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=True,
            outcome=outcome,
            output_published=session.output_published,
        )
        return self.session


class FailingCompleteStore(FakeStore):
    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        raise RuntimeError("audit database unavailable")


class FailingFinalEventStore(FakeStore):
    async def append_event(self, session_id: str, event: dict[str, object]) -> None:
        if event["node"] == "complete_session":
            raise RuntimeError("audit database unavailable")
        await super().append_event(session_id, event)


class FixedClock:
    def __init__(self) -> None:
        self.now = datetime(2026, 6, 6, tzinfo=UTC)

    def datetime(self) -> datetime:
        return self.now

    def monotonic(self) -> float:
        current = self.now
        self.now = self.now + timedelta(seconds=1)
        return current.timestamp()
