from app.llm.ports import LLMPort
from app.llm.types import ChatMessage, ChatRequest


class CompletionService:
    def __init__(self, llm_port: LLMPort) -> None:
        self._llm_port = llm_port

    async def complete(self, messages: list[ChatMessage]) -> str:
        parts: list[str] = []
        async for delta in self._llm_port.stream_chat(ChatRequest(messages=messages)):
            parts.append(delta.content)
        return "".join(parts)
