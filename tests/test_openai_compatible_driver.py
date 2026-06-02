import asyncio
import json

import httpx
import pytest

from app.llm.drivers.openai_compatible import OpenAICompatibleDriver
from app.llm.errors import (
    ProviderConnectionError,
    ProviderHTTPStatusError,
    ProviderMalformedStreamError,
    ProviderTimeoutError,
)
from app.llm.types import ChatMessage, ChatRequest


def _request() -> ChatRequest:
    return ChatRequest(messages=[ChatMessage(role="user", content="hello")])


def test_driver_parses_openai_compatible_sse_text_deltas() -> None:
    async def run() -> list[str]:
        async def handler(request: httpx.Request) -> httpx.Response:
            assert request.url == httpx.URL("http://127.0.0.1:11434/v1/chat/completions")
            body = json.loads(request.content.decode("utf-8"))
            assert body == {
                "model": "llama3.1",
                "messages": [{"role": "user", "content": "hello"}],
                "stream": True,
            }
            payload = (
                'data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n'
                'data: {"choices":[{"delta":{"content":" world"}}]}\n\n'
                "data: [DONE]\n\n"
            )
            return httpx.Response(200, text=payload)

        transport = httpx.MockTransport(handler)
        driver = OpenAICompatibleDriver(
            base_url="http://127.0.0.1:11434/v1",
            api_key="secret",
            model="llama3.1",
            timeout_seconds=30,
            transport=transport,
        )
        deltas = [delta async for delta in driver.stream_chat(_request())]
        return [delta.content for delta in deltas]

    assert asyncio.run(run()) == ["Hello", " world"]


def test_driver_raises_timeout_error() -> None:
    async def run() -> None:
        async def handler(request: httpx.Request) -> httpx.Response:
            raise httpx.ReadTimeout("timed out", request=request)

        driver = OpenAICompatibleDriver(
            base_url="http://127.0.0.1:11434/v1",
            api_key="secret",
            model="llama3.1",
            timeout_seconds=30,
            transport=httpx.MockTransport(handler),
        )
        with pytest.raises(ProviderTimeoutError):
            _ = [delta async for delta in driver.stream_chat(_request())]

    asyncio.run(run())


def test_driver_raises_connection_error() -> None:
    async def run() -> None:
        async def handler(request: httpx.Request) -> httpx.Response:
            raise httpx.ConnectError("connection failed", request=request)

        driver = OpenAICompatibleDriver(
            base_url="http://127.0.0.1:11434/v1",
            api_key="secret",
            model="llama3.1",
            timeout_seconds=30,
            transport=httpx.MockTransport(handler),
        )
        with pytest.raises(ProviderConnectionError):
            _ = [delta async for delta in driver.stream_chat(_request())]

    asyncio.run(run())


def test_driver_raises_http_status_error() -> None:
    async def run() -> None:
        async def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(503, text="upstream unavailable")

        driver = OpenAICompatibleDriver(
            base_url="http://127.0.0.1:11434/v1",
            api_key="secret",
            model="llama3.1",
            timeout_seconds=30,
            transport=httpx.MockTransport(handler),
        )
        with pytest.raises(ProviderHTTPStatusError, match="Provider returned status 503"):
            _ = [delta async for delta in driver.stream_chat(_request())]

    asyncio.run(run())


def test_driver_raises_malformed_stream_error() -> None:
    async def run() -> None:
        async def handler(request: httpx.Request) -> httpx.Response:
            payload = 'data: {"choices":[{"delta":{"content":"ok"}}]}\n\n' "data: not-json\n\n"
            return httpx.Response(200, text=payload)

        driver = OpenAICompatibleDriver(
            base_url="http://127.0.0.1:11434/v1",
            api_key="secret",
            model="llama3.1",
            timeout_seconds=30,
            transport=httpx.MockTransport(handler),
        )
        with pytest.raises(ProviderMalformedStreamError):
            _ = [delta async for delta in driver.stream_chat(_request())]

    asyncio.run(run())
