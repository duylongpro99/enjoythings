import asyncio
from datetime import UTC, datetime

import pytest

from app.fraud.dto import FraudOutcome, FraudScoreRequest, FraudVerdict
from app.fraud.repo.postgres import PostgresFraudSessionStore
from app.fraud.worker import SessionClaimError


def request() -> FraudScoreRequest:
    return FraudScoreRequest(
        schema_version=1,
        event_id="fraud.score.requested:payment-1",
        payment_id="payment-1",
        user_id="private-user",
        from_wallet_id="private-wallet-1",
        to_wallet_id="private-wallet-2",
        amount_cents=1250,
        currency="USD",
        occurred_at=datetime.now(UTC),
        trace_id="trace-1",
    )


def test_claim_creates_session_without_persisting_private_ids() -> None:
    pool = FakePool(
        rows=[
            None,
            {
                "session_id": "session-1",
                "source_event_id": request().event_id,
                "payment_id": request().payment_id,
                "completed_at": None,
                "parsed_verdict_json": {},
                "final_outcome": "",
                "failure_reason": "",
                "output_event_type": "",
                "output_published": False,
            },
        ]
    )

    session = asyncio.run(
        PostgresFraudSessionStore(pool, lease_owner="worker-1").claim_session(request())
    )

    assert session.session_id == "session-1"
    all_args = repr(pool.calls)
    assert "private-user" not in all_args
    assert "private-wallet" not in all_args


def test_claim_rejects_session_owned_by_active_worker() -> None:
    pool = FakePool(rows=[None, None])

    with pytest.raises(SessionClaimError):
        asyncio.run(
            PostgresFraudSessionStore(pool, lease_owner="worker-2").claim_session(request())
        )


def test_complete_persists_flagged_outcome_before_publication() -> None:
    pool = FakePool(
        rows=[
            {
                "session_id": "session-1",
                "source_event_id": request().event_id,
                "payment_id": request().payment_id,
                "completed_at": datetime.now(UTC),
                "parsed_verdict_json": {
                    "risk_score": 0.95,
                    "action": "block",
                    "reason": "velocity",
                    "model_action": "block",
                    "action_normalized": False,
                },
                "final_outcome": "block",
                "failure_reason": "",
                "output_event_type": "fraud.flagged",
                "output_published": False,
            }
        ]
    )
    store = PostgresFraudSessionStore(pool, lease_owner="worker-1")
    session = store._row_to_session(
        {
            "session_id": "session-1",
            "source_event_id": request().event_id,
            "payment_id": request().payment_id,
            "completed_at": None,
            "parsed_verdict_json": {},
            "final_outcome": "",
            "failure_reason": "",
            "output_event_type": "",
            "output_published": False,
        }
    )
    outcome = FraudOutcome(
        action="block",
        verdict=FraudVerdict(0.95, "block", "velocity", "block", False),
    )

    completed = asyncio.run(store.complete_session(session, outcome))

    assert completed.completed is True
    assert completed.output_event_type == "fraud.flagged"
    assert any("completed_at" in sql for sql, _ in pool.calls)


def test_schema_readiness_requires_fraud_sessions_table() -> None:
    ready = PostgresFraudSessionStore(FakePool(values=[True]), lease_owner="worker")
    missing = PostgresFraudSessionStore(FakePool(values=[False]), lease_owner="worker")

    assert asyncio.run(ready.schema_ready()) is True
    assert asyncio.run(missing.schema_ready()) is False


def test_stale_worker_cannot_append_audit_events() -> None:
    pool = FakePool(execute_results=["UPDATE 0"])
    store = PostgresFraudSessionStore(pool, lease_owner="stale-worker")

    with pytest.raises(SessionClaimError):
        asyncio.run(store.append_event("session-1", {"node": "call_llm"}))


class FakePool:
    def __init__(self, rows=None, values=None, execute_results=None) -> None:
        self.rows = list(rows or [])
        self.values = list(values or [])
        self.execute_results = list(execute_results or [])
        self.calls: list[tuple[str, tuple[object, ...]]] = []

    async def fetchrow(self, sql: str, *args):
        self.calls.append((sql, args))
        return self.rows.pop(0)

    async def fetchval(self, sql: str, *args):
        self.calls.append((sql, args))
        return self.values.pop(0)

    async def execute(self, sql: str, *args):
        self.calls.append((sql, args))
        if self.execute_results:
            return self.execute_results.pop(0)
        return "UPDATE 1"

    async def close(self) -> None:
        pass
