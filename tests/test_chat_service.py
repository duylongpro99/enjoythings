import asyncio
import pytest

from app.llm.errors import ProviderConnectionError, ProviderTimeoutError
from app.llm.service import ChatService
from app.llm.types import ChatDelta, ChatMessage, ChatRequest


class RetryStubLLMPort:
    def __init__(self, failures_before_success: int, failure_type: str) -> None:
        self.calls = 0
        self._failures_before_success = failures_before_success
        self._failure_type = failure_type

    async def stream_chat(self, request: ChatRequest):
        self.calls += 1
        assert request.messages and request.messages[0].role == "user"
        assert request.messages[0].content == "hello"

        if self.calls <= self._failures_before_success:
            if self._failure_type == "timeout":
                raise ProviderTimeoutError("timed out")
            raise ProviderConnectionError("connection failed")

        yield ChatDelta(content="ok")


def test_chat_service_retries_timeouts_up_to_three_total_attempts() -> None:
    async def run() -> tuple[list[ChatDelta], int]:
        port = RetryStubLLMPort(failures_before_success=2, failure_type="timeout")
        service = ChatService(port)
        deltas = [delta async for delta in service.stream_reply("hello")]
        return deltas, port.calls

    deltas, calls = asyncio.run(run())

    assert [delta.content for delta in deltas] == ["ok"]
    assert calls == 3


def test_chat_service_stops_after_three_total_timeout_attempts() -> None:
    async def run() -> int:
        port = RetryStubLLMPort(failures_before_success=10, failure_type="timeout")
        service = ChatService(port)
        with pytest.raises(ProviderTimeoutError, match="timed out after 3 attempts"):
            _ = [delta async for delta in service.stream_reply("hello")]
        return port.calls

    calls = asyncio.run(run())
    assert calls == 3


def test_chat_service_does_not_retry_non_timeout_failures() -> None:
    async def run() -> int:
        port = RetryStubLLMPort(failures_before_success=1, failure_type="connection")
        service = ChatService(port)
        with pytest.raises(ProviderConnectionError, match="connection failed"):
            _ = [delta async for delta in service.stream_reply("hello")]
        return port.calls

    calls = asyncio.run(run())
    assert calls == 1


def test_chat_service_builds_provider_agnostic_request() -> None:
    async def run() -> list[ChatRequest]:
        seen_requests: list[ChatRequest] = []

        class InspectingPort:
            async def stream_chat(self, request: ChatRequest):
                seen_requests.append(request)
                yield ChatDelta(content="ok")

        service = ChatService(InspectingPort())
        _ = [delta async for delta in service.stream_reply("hello")]
        return seen_requests

    seen_requests = asyncio.run(run())
    assert seen_requests == [ChatRequest(messages=[ChatMessage(role="user", content="hello")])]
