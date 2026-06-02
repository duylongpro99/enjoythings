import json
from collections.abc import AsyncIterator

import httpx

from app.llm.errors import (
    ProviderConnectionError,
    ProviderHTTPStatusError,
    ProviderMalformedStreamError,
    ProviderTimeoutError,
)
from app.llm.types import ChatDelta, ChatRequest


class OpenAICompatibleDriver:
    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        model: str,
        timeout_seconds: float,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._model = model
        self._timeout = timeout_seconds
        self._transport = transport

    async def stream_chat(self, request: ChatRequest) -> AsyncIterator[ChatDelta]:
        payload = {
            "model": self._model,
            "messages": [
                {
                    "role": message.role,
                    "content": message.content,
                }
                for message in request.messages
            ],
            "stream": True,
        }

        headers = {
            "authorization": f"Bearer {self._api_key}",
            "content-type": "application/json",
        }
        url = f"{self._base_url}/chat/completions"

        try:
            async with httpx.AsyncClient(
                timeout=self._timeout,
                transport=self._transport,
            ) as client:
                async with client.stream("POST", url, json=payload, headers=headers) as response:
                    if response.status_code < 200 or response.status_code >= 300:
                        raise ProviderHTTPStatusError(
                            f"Provider returned status {response.status_code}"
                        )

                    async for line in response.aiter_lines():
                        if not line or not line.startswith("data:"):
                            continue

                        data = line.removeprefix("data:").strip()
                        if data == "[DONE]":
                            break

                        delta_text = _extract_delta_text(data)
                        if delta_text is not None and delta_text != "":
                            yield ChatDelta(content=delta_text)
        except httpx.TimeoutException as exc:
            raise ProviderTimeoutError("request timed out") from exc
        except ProviderHTTPStatusError:
            raise
        except ProviderMalformedStreamError:
            raise
        except httpx.TransportError as exc:
            raise ProviderConnectionError("connection failed") from exc


def _extract_delta_text(data: str) -> str | None:
    try:
        payload = json.loads(data)
    except json.JSONDecodeError as exc:
        raise ProviderMalformedStreamError("provider returned malformed stream data") from exc

    if not isinstance(payload, dict):
        raise ProviderMalformedStreamError("provider returned malformed stream data")

    choices = payload.get("choices")
    if not isinstance(choices, list) or not choices:
        raise ProviderMalformedStreamError("provider returned malformed stream data")

    first_choice = choices[0]
    if not isinstance(first_choice, dict):
        raise ProviderMalformedStreamError("provider returned malformed stream data")

    delta = first_choice.get("delta")
    if not isinstance(delta, dict):
        return None

    content = delta.get("content")
    if content is None:
        return None
    if not isinstance(content, str):
        raise ProviderMalformedStreamError("provider returned malformed stream data")
    return content

