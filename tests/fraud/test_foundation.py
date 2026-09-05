import ast
import asyncio
import json
from pathlib import Path

import pytest

from app.fraud.completion import CompletionService
from app.fraud.config import FraudConfig, FraudConfigError
from app.fraud.guards import guard_prompt
from app.fraud.ports import FraudSessionStore
from app.fraud.validator import validate_verdict
from app.llm.types import ChatDelta, ChatMessage


def test_config_validates_threshold_order_and_bounds() -> None:
    environ = {"FRAUD_DATABASE_URL": "postgres://fraud-db"}
    config = FraudConfig.from_env(environ)

    assert config.score_threshold == 0.75
    assert config.ledger_grpc_addr == "127.0.0.1:9091"
    assert config.verification_grpc_addr == "127.0.0.1:9094"

    with pytest.raises(FraudConfigError, match="lower than"):
        FraudConfig.from_env(
            {
                **environ,
                "FRAUD_SCORE_THRESHOLD": "0.9",
                "FRAUD_BLOCK_THRESHOLD": "0.9",
            }
        )

    with pytest.raises(FraudConfigError, match="FRAUD_METRICS_PORT"):
        FraudConfig.from_env({**environ, "FRAUD_METRICS_PORT": "70000"})


@pytest.mark.parametrize(
    ("environ", "message"),
    [
        ({}, "FRAUD_DATABASE_URL"),
        (
            {"FRAUD_DATABASE_URL": "postgres://fraud-db", "LEDGER_GRPC_ADDR": ""},
            "LEDGER_GRPC_ADDR",
        ),
        (
            {
                "FRAUD_DATABASE_URL": "postgres://fraud-db",
                "VERIFICATION_GRPC_ADDR": " ",
            },
            "VERIFICATION_GRPC_ADDR",
        ),
    ],
)
def test_config_requires_environment_only_database_and_non_empty_grpc_addresses(
    environ: dict[str, str], message: str
) -> None:
    with pytest.raises(FraudConfigError, match=message):
        FraudConfig.from_env(environ)


def test_grpc_tls_defaults_off_and_requires_all_files_when_enabled() -> None:
    base = {"FRAUD_DATABASE_URL": "postgres://fraud-db"}

    disabled = FraudConfig.from_env(base)
    assert disabled.grpc_tls_enabled is False

    with pytest.raises(FraudConfigError, match="FRAUD_GRPC_TLS_CERT_FILE"):
        FraudConfig.from_env({**base, "FRAUD_GRPC_TLS_ENABLED": "true"})

    enabled = FraudConfig.from_env(
        {
            **base,
            "FRAUD_GRPC_TLS_ENABLED": "true",
            "FRAUD_GRPC_TLS_CERT_FILE": "/certs/fraud-worker.crt",
            "FRAUD_GRPC_TLS_KEY_FILE": "/certs/fraud-worker.key",
            "FRAUD_GRPC_TLS_CA_FILE": "/certs/ca.crt",
        }
    )
    assert enabled.grpc_tls_enabled is True
    assert enabled.grpc_tls_ca_file == "/certs/ca.crt"

    with pytest.raises(FraudConfigError, match="numeric settings"):
        FraudConfig.from_env({**base, "FRAUD_GRPC_TLS_ENABLED": "maybe"})


def test_session_store_port_includes_idempotent_event_append_and_completion() -> None:
    assert hasattr(FraudSessionStore, "claim_session")
    assert hasattr(FraudSessionStore, "append_event")
    assert hasattr(FraudSessionStore, "complete_session")


@pytest.mark.parametrize(
    ("prompt", "expected"),
    [
        ("", "prompt_empty"),
        (" \n\t", "prompt_empty"),
        ("payment 11111111-1111-1111-1111-111111111111", "uuid_detected"),
        ("payment_11111111-1111-1111-1111-111111111111_value", "uuid_detected"),
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


@pytest.mark.parametrize(
    ("response", "reason"),
    [
        ("x" * 11, "response_too_large"),
        (json.dumps([]), "invalid_schema"),
        (
            json.dumps({"risk_score": 0.2, "action": "allow", "reason": " "}),
            "invalid_schema",
        ),
        (
            json.dumps({"risk_score": True, "action": "allow", "reason": "normal"}),
            "invalid_score",
        ),
        (
            json.dumps({"risk_score": float("nan"), "action": "allow", "reason": "normal"}),
            "invalid_score",
        ),
        (
            json.dumps(
                {"risk_score": -0.011, "action": "allow", "reason": "normal"}
            ),
            "invalid_score",
        ),
        (
            json.dumps(
                {"risk_score": 0.2, "action": "allow", "reason": "contains user_id"}
            ),
            "sensitive_output",
        ),
    ],
)
def test_validator_covers_all_rejection_categories(response: str, reason: str) -> None:
    result = validate_verdict(
        response,
        score_threshold=0.75,
        block_threshold=0.9,
        max_chars=10 if reason == "response_too_large" else 4000,
        sensitive_keys=("user_id",),
    )

    assert result.rejection_reason == reason
    assert result.corrective_prompt.endswith(f"{reason}.")


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


def test_completion_service_accepts_any_llm_port_without_provider_registry_changes() -> None:
    class FakeProvider:
        def __init__(self, content: str) -> None:
            self.content = content

        async def stream_chat(self, request):
            yield ChatDelta(content=self.content)

    async def run() -> tuple[str, str]:
        messages = [ChatMessage(role="user", content="prompt")]
        first = await CompletionService(FakeProvider("first")).complete(messages)
        second = await CompletionService(FakeProvider("second")).complete(messages)
        return first, second

    assert asyncio.run(run()) == ("first", "second")


def test_core_fraud_domain_has_no_infrastructure_or_provider_imports() -> None:
    forbidden_roots = {
        "aiokafka",
        "anthropic",
        "asyncpg",
        "cohere",
        "google",
        "grpc",
        "kafka",
        "opentelemetry",
        "openai",
        "psycopg",
    }
    core_modules = (
        "completion.py",
        "config.py",
        "dto.py",
        "guards.py",
        "instruction.py",
        "ports.py",
        "service.py",
        "validator.py",
        "worker.py",
    )

    for module_name in core_modules:
        path = Path("app/fraud") / module_name
        tree = ast.parse(path.read_text())
        imported_roots = {
            alias.name.split(".", 1)[0]
            for node in ast.walk(tree)
            if isinstance(node, ast.Import)
            for alias in node.names
        } | {
            node.module.split(".", 1)[0]
            for node in ast.walk(tree)
            if isinstance(node, ast.ImportFrom) and node.module
        }
        assert imported_roots.isdisjoint(forbidden_roots), path
