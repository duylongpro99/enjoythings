from collections.abc import AsyncIterator
from typing import Protocol

from app.llm.types import ChatDelta, ChatRequest


class LLMPort(Protocol):
    # Drivers implement this as an async generator, so the method itself
    # returns the iterator rather than a coroutine that yields one.
    def stream_chat(self, request: ChatRequest) -> AsyncIterator[ChatDelta]:
        """Yield provider-agnostic content deltas."""

