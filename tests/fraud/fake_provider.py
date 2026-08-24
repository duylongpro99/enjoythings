"""Deterministic OpenAI-compatible provider server for Phase 4 end-to-end tests.

The server hosts one scripted endpoint per provider id so provider switching is
exercised through configuration only. Captured requests let tests assert that no
raw identifier ever reaches a model.
"""

import asyncio
import json
from contextlib import asynccontextmanager
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field

import uvicorn

STARTUP_TIMEOUT_SECONDS = 10.0
API_KEY_ENV = "FAKE_PROVIDER_API_KEY"
API_KEY = "fake-provider-key"


@dataclass(frozen=True)
class CapturedRequest:
    provider_id: str
    model: str
    messages: tuple[tuple[str, str], ...]

    @property
    def text(self) -> str:
        return "\n".join(content for _, content in self.messages)


@dataclass
class ScriptedProvider:
    provider_id: str
    model: str
    responses: list[str]
    requests: list[CapturedRequest] = field(default_factory=list)
    _served: int = 0

    @property
    def call_count(self) -> int:
        return len(self.requests)

    def next_response(self) -> str | None:
        if self._served >= len(self.responses):
            return None
        response = self.responses[self._served]
        self._served += 1
        return response


class FakeProviderServer:
    """ASGI app routing ``/{provider_id}/v1/chat/completions`` to scripted replies."""

    def __init__(self, providers: Mapping[str, Sequence[str]], *, model: str = "fake-model") -> None:
        self._providers = {
            provider_id: ScriptedProvider(provider_id, model, list(responses))
            for provider_id, responses in providers.items()
        }
        self.port = 0

    @property
    def provider_ids(self) -> tuple[str, ...]:
        return tuple(self._providers)

    def provider(self, provider_id: str) -> ScriptedProvider:
        return self._providers[provider_id]

    def requests(self, provider_id: str) -> list[CapturedRequest]:
        return self._providers[provider_id].requests

    def call_count(self, provider_id: str) -> int:
        return self._providers[provider_id].call_count

    def base_url(self, provider_id: str) -> str:
        return f"http://127.0.0.1:{self.port}/{provider_id}/v1"

    def registry_env(self, default_provider_id: str) -> dict[str, str]:
        providers = [
            {
                "id": provider.provider_id,
                "driver_type": "openai_compatible",
                "base_url": self.base_url(provider.provider_id),
                "api_key_env": API_KEY_ENV,
                "model": provider.model,
                "timeout_seconds": 5,
            }
            for provider in self._providers.values()
        ]
        return {
            "LLM_PROVIDERS_JSON": json.dumps({"providers": providers}),
            "LLM_DEFAULT_PROVIDER": default_provider_id,
            API_KEY_ENV: API_KEY,
        }

    async def __call__(self, scope, receive, send) -> None:
        if scope["type"] != "http":
            return
        body = await _read_body(receive)
        provider = self._route(scope["path"])
        if provider is None:
            await _send(send, 404, b"unknown provider endpoint")
            return
        payload = json.loads(body)
        provider.requests.append(
            CapturedRequest(
                provider_id=provider.provider_id,
                model=str(payload.get("model", "")),
                messages=tuple(
                    (str(message.get("role", "")), str(message.get("content", "")))
                    for message in payload.get("messages", [])
                ),
            )
        )
        response = provider.next_response()
        if response is None:
            await _send(send, 500, b"no scripted response remaining")
            return
        await _send(send, 200, _sse(response), content_type=b"text/event-stream")

    def _route(self, path: str) -> ScriptedProvider | None:
        if not path.endswith("/v1/chat/completions"):
            return None
        return self._providers.get(path.strip("/").split("/")[0])


@asynccontextmanager
async def running_provider_server(
    providers: Mapping[str, Sequence[str]], *, model: str = "fake-model"
):
    """Run the fake provider on an ephemeral port for the duration of the block."""

    app = FakeProviderServer(providers, model=model)
    config = uvicorn.Config(app, host="127.0.0.1", port=0, log_level="error", lifespan="off")
    server = uvicorn.Server(config)
    serve_task = asyncio.create_task(server.serve())
    try:
        app.port = await _await_port(server, serve_task)
        yield app
    finally:
        server.should_exit = True
        await serve_task


async def _await_port(server: uvicorn.Server, serve_task: asyncio.Task) -> int:
    deadline = asyncio.get_running_loop().time() + STARTUP_TIMEOUT_SECONDS
    while not server.started:
        if serve_task.done():
            await serve_task
            raise RuntimeError("fake provider server stopped before startup")
        if asyncio.get_running_loop().time() > deadline:
            raise TimeoutError("fake provider server did not start")
        await asyncio.sleep(0.01)
    return int(server.servers[0].sockets[0].getsockname()[1])


async def _read_body(receive) -> bytes:
    body = b""
    while True:
        message = await receive()
        body += message.get("body", b"")
        if not message.get("more_body", False):
            return body


async def _send(send, status: int, body: bytes, content_type: bytes = b"text/plain") -> None:
    await send(
        {
            "type": "http.response.start",
            "status": status,
            "headers": [(b"content-type", content_type)],
        }
    )
    await send({"type": "http.response.body", "body": body})


def _sse(content: str) -> bytes:
    chunk = json.dumps({"choices": [{"delta": {"content": content}}]}, separators=(",", ":"))
    return f"data: {chunk}\n\ndata: [DONE]\n\n".encode()
