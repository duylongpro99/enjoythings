from collections.abc import AsyncIterator

from app.llm.errors import ProviderTimeoutError
from app.llm.ports import LLMPort
from app.llm.types import ChatDelta, ChatMessage, ChatRequest

MAX_TIMEOUT_ATTEMPTS = 3


class ChatService:
    def __init__(self, llm_port: LLMPort) -> None:
        self._llm_port = llm_port

    async def stream_reply(self, message: str) -> AsyncIterator[ChatDelta]:
        request = ChatRequest(messages=[ChatMessage(role="user", content=message)])

        for attempt in range(1, MAX_TIMEOUT_ATTEMPTS + 1):
            try:
                async for delta in self._llm_port.stream_chat(request):
                    yield delta
                return
            except ProviderTimeoutError as exc:
                if attempt == MAX_TIMEOUT_ATTEMPTS:
                    raise ProviderTimeoutError(
                        "Provider timed out after 3 attempts"
                    ) from exc

