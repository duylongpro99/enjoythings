"""Phase 4 fraud end-to-end scenarios driven through a real OpenAI-compatible provider."""

import asyncio

from app.fraud.worker import ConsumerDecision
from tests.fraud.e2e_harness import (
    ALLOW_RESPONSE,
    BLOCK_RESPONSE,
    FLAG_RESPONSE,
    MALFORMED_RESPONSE,
    PAYMENT_ID,
    PRIMARY_PROVIDER,
    RAW_IDENTIFIERS,
    SECONDARY_PROVIDER,
    UUID_RESPONSE,
    FailingAuditStore,
    StubFraudData,
    run_fraud_scenario,
    score_request_payload,
)

CONFLICTING_RESPONSE = '{"risk_score":0.95,"action":"allow","reason":"model disagrees"}'


def test_worker_can_decode_the_compression_the_go_producers_use() -> None:
    """franz-go batches fraud.score.requested with snappy by default."""

    from aiokafka.codec import has_snappy

    assert has_snappy(), "aiokafka needs the snappy codec to read Go producer batches"


def test_low_risk_payment_completes_without_flagged_event() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[ALLOW_RESPONSE]))

    assert result.decision == ConsumerDecision.COMMIT
    assert result.published == []
    assert result.server.call_count(PRIMARY_PROVIDER) == 1
    assert result.audit_event("complete_session", action="allow") is not None


def test_high_risk_payment_publishes_one_flagged_event() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[FLAG_RESPONSE]))

    assert result.decision == ConsumerDecision.COMMIT
    assert result.topics == ["fraud.flagged"]
    flagged = result.events_on("fraud.flagged")[0]
    assert flagged["action"] == "flag"
    assert flagged["payment_id"] == PAYMENT_ID
    assert flagged["source_event_id"] == f"fraud.score.requested:{PAYMENT_ID}"
    assert flagged["provider_id"] == PRIMARY_PROVIDER
    metrics = result.metrics_text
    assert f'fraud_transactions_scored_total{{action="flag",provider="{PRIMARY_PROVIDER}"}} 1.0' in metrics
    assert 'fraud_events_published_total{outcome="success",topic="fraud.flagged"} 1.0' in metrics


def test_block_verdict_preserves_action_in_flagged_event() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[BLOCK_RESPONSE]))

    flagged = result.events_on("fraud.flagged")[0]
    assert flagged["action"] == "block"
    assert flagged["risk_score"] == 0.95


def test_malformed_then_valid_response_retries_once_and_stores_verdict() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[MALFORMED_RESPONSE, FLAG_RESPONSE]))

    assert result.decision == ConsumerDecision.COMMIT
    assert result.server.call_count(PRIMARY_PROVIDER) == 2
    assert result.audit_event("validate_verdict", outcome="rejected", attempt=1) is not None
    assert result.audit_event("validate_verdict", outcome="accepted", attempt=2) is not None
    assert result.events_on("fraud.flagged")[0]["action"] == "flag"
    assert 'fraud_callback_rejections_total{callback="output_validator",reason="invalid_json"} 1.0' in result.metrics_text


def test_two_malformed_responses_publish_fraud_error_and_stay_fail_open() -> None:
    result = asyncio.run(
        run_fraud_scenario(responses=[MALFORMED_RESPONSE, MALFORMED_RESPONSE])
    )

    assert result.decision == ConsumerDecision.COMMIT
    assert result.topics == ["fraud.error"]
    assert result.events_on("fraud.error")[0]["reason_code"] == "validation_failed"
    assert result.server.call_count(PRIMARY_PROVIDER) == 2


def test_prompt_with_raw_uuid_is_rejected_before_the_provider_call() -> None:
    data = StubFraudData(kyc_status=f"verified-by-{RAW_IDENTIFIERS[0]}")

    result = asyncio.run(run_fraud_scenario(responses=[FLAG_RESPONSE], data=data))

    assert result.server.call_count(PRIMARY_PROVIDER) == 0
    assert result.events_on("fraud.error")[0]["reason_code"] == "prompt_rejected"
    guard = result.audit_event("input_guard", outcome="rejected")
    assert guard is not None
    assert guard["rejection_code"] == "uuid_detected"
    assert guard["rejection_phase"] == "before"


def test_model_response_with_uuid_is_rejected_and_retried() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[UUID_RESPONSE, ALLOW_RESPONSE]))

    assert result.decision == ConsumerDecision.COMMIT
    assert result.server.call_count(PRIMARY_PROVIDER) == 2
    rejection = result.audit_event("validate_verdict", outcome="rejected", attempt=1)
    assert rejection is not None
    assert rejection["rejection_code"] == "sensitive_output"
    assert result.published == []
    assert result.audit_event("complete_session", action="allow") is not None


def test_enrichment_unavailable_publishes_fraud_error_without_flagging() -> None:
    result = asyncio.run(
        run_fraud_scenario(responses=[FLAG_RESPONSE], data=StubFraudData(unavailable=True))
    )

    assert result.decision == ConsumerDecision.COMMIT
    assert result.topics == ["fraud.error"]
    assert result.events_on("fraud.error")[0]["reason_code"] == "enrichment_failed"
    assert result.server.call_count(PRIMARY_PROVIDER) == 0
    assert 'fraud_enrichment_calls_total{method="history",outcome="failure"} 1.0' in result.metrics_text


def test_duplicate_scoring_request_scores_once_and_publishes_once() -> None:
    payload = score_request_payload()

    result = asyncio.run(
        run_fraud_scenario(responses=[FLAG_RESPONSE], payloads=[payload, payload])
    )

    assert result.decisions == [ConsumerDecision.COMMIT, ConsumerDecision.COMMIT]
    assert result.server.call_count(PRIMARY_PROVIDER) == 1
    assert result.topics == ["fraud.flagged"]


def test_provider_switch_is_configuration_only() -> None:
    result = asyncio.run(
        run_fraud_scenario(
            providers={
                PRIMARY_PROVIDER: [ALLOW_RESPONSE],
                SECONDARY_PROVIDER: [BLOCK_RESPONSE],
            },
            default_provider=SECONDARY_PROVIDER,
        )
    )

    assert result.server.call_count(PRIMARY_PROVIDER) == 0
    assert result.server.call_count(SECONDARY_PROVIDER) == 1
    assert result.events_on("fraud.flagged")[0]["provider_id"] == SECONDARY_PROVIDER


def test_conflicting_model_action_uses_the_canonical_score_derived_action() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[CONFLICTING_RESPONSE]))

    flagged = result.events_on("fraud.flagged")[0]
    assert flagged["action"] == "block"
    accepted = result.audit_event("validate_verdict", outcome="accepted")
    assert accepted is not None
    assert accepted["model_action"] == "allow"
    assert accepted["action_normalized"] is True


def test_audit_session_records_enrichment_response_verdict_and_outcome() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[FLAG_RESPONSE]))

    assert result.audit_nodes() == [
        "create_session",
        "build_sanitized_context",
        "enrich_transaction",
        "build_prompt",
        "input_guard",
        "call_llm",
        "validate_verdict",
        "complete_session",
    ]
    enrichment = result.audit_event("enrich_transaction")
    assert enrichment is not None
    assert enrichment["sanitized_facts"]["sender_kyc_status"] == "verified"
    assert enrichment["enrichment"] == {
        "history_count": 1,
        "kyc_available": True,
        "velocity_available": True,
    }
    call = result.audit_event("call_llm", outcome="completed")
    assert call is not None
    assert call["raw_response"] == FLAG_RESPONSE
    assert call["provider_id"] == PRIMARY_PROVIDER
    assert result.audit_event("complete_session", action="flag") is not None


def test_no_raw_identifier_reaches_the_provider_events_or_audit_rows() -> None:
    result = asyncio.run(run_fraud_scenario(responses=[FLAG_RESPONSE]))

    exposed = result.sensitive_text()
    audit = str(result.audit_events())
    for identifier in RAW_IDENTIFIERS:
        assert identifier not in exposed
        assert identifier not in audit
    for key in ("user_id", "wallet_id", "from_wallet_id", "to_wallet_id"):
        assert key not in result.server.requests(PRIMARY_PROVIDER)[0].text
    assert result.events_on("fraud.flagged")[0]["payment_id"] == PAYMENT_ID


def test_audit_database_unavailable_keeps_the_request_retryable() -> None:
    result = asyncio.run(
        run_fraud_scenario(responses=[FLAG_RESPONSE], store=FailingAuditStore(fail_after=1))
    )

    assert result.decision == ConsumerDecision.RETRY
    assert result.topics == ["fraud.error"]
    assert result.events_on("fraud.error")[0]["reason_code"] == "audit_failed"
