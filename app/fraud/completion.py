from app.llm.ports import LLMPort
from app.llm.types import ChatMessage, ChatRequest


class CompletionService:
    """Collects a full completion from the streaming provider port.

    ``provider_id`` and ``model_id`` label audit records and published events,
    so they are part of the service rather than attributes bolted on at wiring
    time.
    """

    def __init__(self, llm_port: LLMPort, provider_id: str = "", model_id: str = "") -> None:
        self._llm_port = llm_port
        self.provider_id = provider_id
        self.model_id = model_id

    async def complete(self, messages: list[ChatMessage]) -> str:
        parts: list[str] = []
        async for delta in self._llm_port.stream_chat(ChatRequest(messages=messages)):
            parts.append(delta.content)
        return "".join(parts)
