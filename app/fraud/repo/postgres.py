import json
from dataclasses import asdict
from typing import Any
from uuid import uuid4

from app.fraud.dto import FraudOutcome, FraudScoreRequest, FraudSession, FraudVerdict
from app.fraud.worker import SessionClaimError


class PostgresFraudSessionStore:
    def __init__(self, pool: Any, *, lease_owner: str, lease_seconds: int = 60) -> None:
        self._pool = pool
        self._lease_owner = lease_owner
        self._lease_seconds = lease_seconds

    async def claim_session(self, request: FraudScoreRequest) -> FraudSession:
        await self._pool.fetchrow(
            """
            INSERT INTO fraud_sessions (session_id, source_event_id, payment_id)
            VALUES ($1, $2, $3)
            ON CONFLICT (source_event_id) DO NOTHING
            RETURNING session_id
            """,
            str(uuid4()),
            request.event_id,
            request.payment_id,
        )
        row = await self._pool.fetchrow(
            """
            UPDATE fraud_sessions
            SET lease_owner = $2,
                lease_expires_at = now() + ($3 * interval '1 second')
            WHERE source_event_id = $1
              AND (
                completed_at IS NOT NULL
                OR lease_owner = $2
                OR lease_expires_at IS NULL
                OR lease_expires_at <= now()
              )
            RETURNING *
            """,
            request.event_id,
            self._lease_owner,
            self._lease_seconds,
        )
        if row is None:
            raise SessionClaimError("fraud session is leased by another worker")
        return self._row_to_session(row)

    async def append_event(self, session_id: str, event: dict[str, object]) -> None:
        raw_response = event.get("raw_response")
        provider_id = event.get("provider_id")
        model_id = event.get("model_id")
        sanitized_facts = event.get("sanitized_facts")
        enrichment = event.get("enrichment")
        result = await self._pool.execute(
            """
            UPDATE fraud_sessions
            SET events_json = events_json || $2::jsonb,
                raw_llm_response = CASE WHEN $3 = '' THEN raw_llm_response ELSE $3 END,
                provider_id = CASE WHEN $4 = '' THEN provider_id ELSE $4 END,
                model_id = CASE WHEN $5 = '' THEN model_id ELSE $5 END,
                sanitized_facts_json = CASE WHEN $6 = '{}' THEN sanitized_facts_json ELSE $6::jsonb END,
                enrichment_json = CASE WHEN $7 = '{}' THEN enrichment_json ELSE $7::jsonb END
            WHERE session_id = $1
              AND lease_owner = $8
              AND lease_expires_at > now()
            """,
            session_id,
            json.dumps([event]),
            raw_response if isinstance(raw_response, str) else "",
            provider_id if isinstance(provider_id, str) else "",
            model_id if isinstance(model_id, str) else "",
            json.dumps(sanitized_facts) if isinstance(sanitized_facts, dict) else "{}",
            json.dumps(enrichment) if isinstance(enrichment, dict) else "{}",
            self._lease_owner,
        )
        if result == "UPDATE 0":
            raise SessionClaimError("fraud session lease was lost during audit append")

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        verdict = asdict(outcome.verdict) if outcome.verdict else {}
        output_type = _output_event_type(outcome)
        row = await self._pool.fetchrow(
            """
            UPDATE fraud_sessions
            SET parsed_verdict_json = $2::jsonb,
                final_outcome = $3,
                failure_reason = $4,
                output_event_type = $5,
                completed_at = now()
            WHERE session_id = $1
              AND lease_owner = $6
              AND lease_expires_at > now()
            RETURNING *
            """,
            session.session_id,
            json.dumps(verdict),
            outcome.action or "fail_open",
            (outcome.reason_code or "")[:100],
            output_type,
            self._lease_owner,
        )
        if row is None:
            raise SessionClaimError("fraud session lease was lost before completion")
        return self._row_to_session(row)

    async def mark_published(self, session: FraudSession) -> FraudSession:
        row = await self._pool.fetchrow(
            """
            UPDATE fraud_sessions SET output_published = TRUE
            WHERE session_id = $1
              AND lease_owner = $2
              AND lease_expires_at > now()
            RETURNING *
            """,
            session.session_id,
            self._lease_owner,
        )
        if row is None:
            raise RuntimeError("fraud session missing while marking publication")
        return self._row_to_session(row)

    async def renew_lease(self, session: FraudSession) -> None:
        result = await self._pool.execute(
            """
            UPDATE fraud_sessions
            SET lease_expires_at = now() + ($3 * interval '1 second')
            WHERE session_id = $1 AND lease_owner = $2 AND completed_at IS NULL
            """,
            session.session_id,
            self._lease_owner,
            self._lease_seconds,
        )
        if result == "UPDATE 0":
            raise SessionClaimError("fraud session lease renewal failed")

    async def release_lease(self, session: FraudSession) -> None:
        await self._pool.execute(
            """
            UPDATE fraud_sessions SET lease_owner = '', lease_expires_at = NULL
            WHERE session_id = $1 AND lease_owner = $2
            """,
            session.session_id,
            self._lease_owner,
        )

    async def database_ready(self) -> bool:
        try:
            return await self._pool.fetchval("SELECT 1") == 1
        except Exception:
            return False

    async def schema_ready(self) -> bool:
        try:
            return bool(
                await self._pool.fetchval(
                    "SELECT to_regclass('public.fraud_sessions') IS NOT NULL"
                )
            )
        except Exception:
            return False

    async def close(self) -> None:
        await self._pool.close()

    def _row_to_session(self, row: Any) -> FraudSession:
        verdict_json = _json_value(row.get("parsed_verdict_json"))
        failure_reason = row.get("failure_reason") or ""
        final_outcome = row.get("final_outcome") or ""
        verdict = FraudVerdict(**verdict_json) if verdict_json else None
        outcome = None
        if row.get("completed_at") is not None:
            outcome = FraudOutcome(
                action=final_outcome if final_outcome in ("allow", "flag", "block") else None,
                verdict=verdict,
                reason_code=failure_reason or None,
            )
        return FraudSession(
            session_id=row["session_id"],
            source_event_id=row["source_event_id"],
            payment_id=row["payment_id"],
            completed=row.get("completed_at") is not None,
            outcome=outcome,
            output_event_type=row.get("output_event_type") or "",
            output_published=bool(row.get("output_published")),
        )


def _json_value(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        value = json.loads(value)
    return value if isinstance(value, dict) else {}


def _output_event_type(outcome: FraudOutcome) -> str:
    if outcome.action in ("flag", "block"):
        return "fraud.flagged"
    if outcome.reason_code:
        return "fraud.error"
    return ""
