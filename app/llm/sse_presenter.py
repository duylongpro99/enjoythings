import json
from collections.abc import AsyncIterator

from app.llm.types import ChatDelta


def _chunk_payload(content: str | None, finish_reason: str | None) -> dict:
    delta = {"content": content} if content is not None else {}
    return {
        "id": "chatcmpl-local",
        "object": "chat.completion.chunk",
        "choices": [
            {
                "index": 0,
                "delta": delta,
                "finish_reason": finish_reason,
            }
        ],
    }


async def stream_openai_chat_sse(deltas: AsyncIterator[ChatDelta]) -> AsyncIterator[str]:
    async for delta in deltas:
        chunk = _chunk_payload(content=delta.content, finish_reason=None)
        yield f"data: {json.dumps(chunk, separators=(',', ':'))}\n\n"

    final_chunk = _chunk_payload(content=None, finish_reason="stop")
    yield f"data: {json.dumps(final_chunk, separators=(',', ':'))}\n\n"
    yield "data: [DONE]\n\n"

