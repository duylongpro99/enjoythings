import asyncio
import json

import pytest

from app.fraud.completion import CompletionService
from app.fraud.config import FraudConfig, FraudConfigError
from app.fraud.guards import guard_prompt
from app.fraud.validator import validate_verdict
from app.llm.types import ChatDelta, ChatMessage


def test_config_validates_threshold_order_and_bounds() -> None:
    assert FraudConfig.from_env({}).score_threshold == 0.75

    with pytest.raises(FraudConfigError, match="lower than"):
        FraudConfig.from_env(
            {"FRAUD_SCORE_THRESHOLD": "0.9", "FRAUD_BLOCK_THRESHOLD": "0.9"}
        )

    with pytest.raises(FraudConfigError, match="FRAUD_METRICS_PORT"):
        FraudConfig.from_env({"FRAUD_METRICS_PORT": "70000"})


@pytest.mark.parametrize(
    ("prompt", "expected"),
    [
        ("", "prompt_empty"),
        ("payment 11111111-1111-1111-1111-111111111111", "uuid_detected"),
        ("facts include user_id", "sensitive_key_detected"),
        ("x" * 11, "prompt_too_large"),
    ],
)
def test_guard_returns_bounded_rejection_codes(prompt: str, expected: str) -> None:
    assert guard_prompt(prompt, max_chars=10, sensitive_keys=("user_id",)) == expected


def test_validator_derives_canonical_action_and_preserves_model_action() -> None:
    result = validate_verdict(
        json.dumps({"risk_score": 0.8, "action": "allow", "reason": "unusual velocity"}),
        score_threshold=0.75,
        block_threshold=0.9,
        max_chars=4000,
        sensitive_keys=("user_id",),
    )

    assert result.rejection_reason is None
    assert result.verdict is not None
    assert result.verdict.action == "flag"
    assert result.verdict.model_action == "allow"
    assert result.verdict.action_normalized is True


@pytest.mark.parametrize(
    ("response", "reason"),
    [
        ("{", "invalid_json"),
        (json.dumps({"risk_score": 0.2, "action": "review", "reason": "x"}), "invalid_action"),
        (json.dumps({"risk_score": 2, "action": "block", "reason": "x"}), "invalid_score"),
        (
            json.dumps(
                {
                    "risk_score": 0.2,
                    "action": "allow",
                    "reason": "user 11111111-1111-1111-1111-111111111111",
                }
            ),
            "sensitive_output",
        ),
    ],
)
def test_validator_returns_bounded_rejection_codes(response: str, reason: str) -> None:
    result = validate_verdict(
        response,
        score_threshold=0.75,
        block_threshold=0.9,
        max_chars=4000,
        sensitive_keys=("user_id",),
    )
    assert result.verdict is None
    assert result.rejection_reason == reason
    assert reason in result.corrective_prompt
    assert response not in result.corrective_prompt


def test_validator_clamps_score_within_tolerance() -> None:
    result = validate_verdict(
        json.dumps({"risk_score": 1.005, "action": "block", "reason": "high risk"}),
        score_threshold=0.75,
        block_threshold=0.9,
        max_chars=4000,
        sensitive_keys=(),
    )
    assert result.verdict is not None
    assert result.verdict.risk_score == 1.0


def test_completion_service_joins_deltas_in_one_provider_call() -> None:
    class FakeLLM:
        calls = 0

        async def stream_chat(self, request):
            self.calls += 1
            assert request.messages == [ChatMessage(role="user", content="prompt")]
            yield ChatDelta(content='{"risk_')
            yield ChatDelta(content='score": 0.1}')

    async def run() -> tuple[str, int]:
        llm = FakeLLM()
        response = await CompletionService(llm).complete(
            [ChatMessage(role="user", content="prompt")]
        )
        return response, llm.calls

    response, calls = asyncio.run(run())
    assert response == '{"risk_score": 0.1}'
    assert calls == 1
