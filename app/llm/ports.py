from collections.abc import AsyncIterator
from typing import Protocol

from app.llm.types import ChatDelta, ChatRequest


class LLMPort(Protocol):
    async def stream_chat(self, request: ChatRequest) -> AsyncIterator[ChatDelta]:
        """Yield provider-agnostic content deltas."""

