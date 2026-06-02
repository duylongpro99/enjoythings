import json
import os
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.llm.errors import ProviderConfigError, ProviderConnectionError, ProviderTimeoutError
from app.llm.service import ChatService
from app.llm.types import ChatDelta, ChatMessage, ChatRequest
from app.main import create_app


class FakeLLMPort:
    def __init__(self, deltas: list[str], fail_after_index: int | None = None) -> None:
        self._deltas = deltas
        self._fail_after_index = fail_after_index

    async def stream_chat(self, request: ChatRequest):
        assert request.messages == [ChatMessage(role="user", content="hello world")]

        for idx, delta in enumerate(self._deltas):
            yield ChatDelta(content=delta)
            if self._fail_after_index is not None and idx == self._fail_after_index:
                raise ProviderConnectionError("provider disconnected")


class AlwaysTimeoutLLMPort:
    async def stream_chat(self, request: ChatRequest):
        raise ProviderTimeoutError("request timed out")
        yield  # pragma: no cover


def _event_payloads(lines: list[str]) -> list[str]:
    return [line.removeprefix("data: ") for line in lines if line.startswith("data: ")]


def test_chat_requires_non_empty_message() -> None:
    app = create_app(chat_service=ChatService(FakeLLMPort(["Echo: hello world"])))
    client = TestClient(app)
    response = client.post("/chat", json={"message": ""})

    assert response.status_code == 422


def test_chat_streams_openai_style_sse_chunks() -> None:
    app = create_app(chat_service=ChatService(FakeLLMPort(["Echo: ", "hello ", "world"])))
    client = TestClient(app)

    with client.stream("POST", "/chat", json={"message": "hello world"}) as response:
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("text/event-stream")
        lines = [line for line in response.iter_lines() if line]

    payloads = _event_payloads(lines)
    assert payloads[-1] == "[DONE]"

    json_payloads = [json.loads(payload) for payload in payloads[:-1]]
    assert len(json_payloads) >= 2

    first_chunk = json_payloads[0]
    assert first_chunk["id"] == "chatcmpl-local"
    assert first_chunk["object"] == "chat.completion.chunk"
    assert first_chunk["choices"][0]["index"] == 0
    assert "content" in first_chunk["choices"][0]["delta"]
    assert first_chunk["choices"][0]["finish_reason"] is None

    final_chunk = json_payloads[-1]
    assert final_chunk["choices"][0]["delta"] == {}
    assert final_chunk["choices"][0]["finish_reason"] == "stop"


def test_chat_returns_timeout_error_before_stream_starts() -> None:
    app = create_app(chat_service=ChatService(AlwaysTimeoutLLMPort()))
    client = TestClient(app)

    response = client.post("/chat", json={"message": "hello world"})

    assert response.status_code == 504
    assert response.json() == {"error": "Provider timed out after 3 attempts"}


def test_chat_does_not_emit_done_after_midstream_failure() -> None:
    app = create_app(
        chat_service=ChatService(
            FakeLLMPort(["first chunk"], fail_after_index=0),
        )
    )
    client = TestClient(app, raise_server_exceptions=False)

    with client.stream("POST", "/chat", json={"message": "hello world"}) as response:
        assert response.status_code == 200
        lines = [line for line in response.iter_lines() if line]

    payloads = _event_payloads(lines)
    assert "[DONE]" not in payloads


def test_create_app_loads_dotenv_for_all_backend_config(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.chdir(tmp_path)
    monkeypatch.delenv("THIRD_PARTY_API_KEY", raising=False)
    tmp_path.joinpath(".env").write_text(
        "THIRD_PARTY_API_KEY=secret-for-other-code\n",
        encoding="utf-8",
    )

    create_app(chat_service=ChatService(FakeLLMPort(["ok"])))

    assert os.environ["THIRD_PARTY_API_KEY"] == "secret-for-other-code"


def test_create_app_keeps_existing_env_over_dotenv(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("THIRD_PARTY_API_KEY", "secret-from-real-env")
    tmp_path.joinpath(".env").write_text(
        "THIRD_PARTY_API_KEY=secret-from-dotenv\n",
        encoding="utf-8",
    )

    create_app(chat_service=ChatService(FakeLLMPort(["ok"])))

    assert os.environ["THIRD_PARTY_API_KEY"] == "secret-from-real-env"


def test_app_startup_fails_when_provider_config_missing(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.chdir(tmp_path)
    monkeypatch.delenv("LLM_PROVIDERS_JSON", raising=False)
    monkeypatch.delenv("LLM_DEFAULT_PROVIDER", raising=False)

    with pytest.raises(ProviderConfigError, match="LLM_PROVIDERS_JSON is required"):
        with TestClient(create_app()):
            pass
