# FastAPI Chat Stream Design

Date: 2026-05-31

## Goal

Create a minimal FastAPI server managed by `uv` that exposes a chat endpoint returning a streamed response. The stream should use OpenAI-style Server-Sent Events so clients receive tokens progressively instead of waiting for a complete JSON response.

When this spec is finished and implemented, the project must achieve these outcomes:

- A developer can install dependencies into `.venv` with `uv sync`.
- A developer can start the API with `uv run uvicorn app.main:app --reload`.
- `POST /chat` accepts a non-empty user message.
- `POST /chat` returns a real streaming HTTP response, not a completed JSON response.
- The stream is compatible with OpenAI-style SSE clients: each event uses `data: {...}\n\n`, and the stream ends with `data: [DONE]\n\n`.
- The streamed JSON payloads use chat completion chunk fields including `id`, `object`, `choices`, `delta.content`, and `finish_reason`.
- The behavior can be verified with `curl -N` by seeing multiple chunks arrive progressively.

## Project Setup

The repository is currently empty and is not a git repository. The server will be scaffolded as a small Python package with:

- `pyproject.toml` for project metadata and dependencies.
- `app/main.py` for the FastAPI application.
- `app/__init__.py` to make the app importable.

Dependencies:

- `fastapi`
- `uvicorn`

The project will be run through `uv` and its `.venv` environment:

```bash
uv sync
uv run uvicorn app.main:app --reload
```

## API

### `POST /chat`

Request body:

```json
{
  "message": "hello"
}
```

The `message` field is required and must be a non-empty string.

Response:

- HTTP 200
- `text/event-stream`
- data-only Server-Sent Events

Each streamed event will be sent as:

```text
data: {"id":"chatcmpl-local","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

```

The JSON inside each `data:` event follows the shape of an OpenAI chat completion stream chunk:

```json
{
  "id": "chatcmpl-local",
  "object": "chat.completion.chunk",
  "choices": [
    {
      "index": 0,
      "delta": {
        "content": "Hello"
      },
      "finish_reason": null
    }
  ]
}
```

The stream ends with a final chunk whose `delta` is empty and whose `finish_reason` is `"stop"`, followed by:

```text
data: [DONE]

```

## Streaming Behavior

The first implementation will generate a deterministic mock assistant response derived from the user's message. This avoids external service dependencies while proving the streaming contract works end to end.

Chunks will be yielded asynchronously with a small delay between tokens so clients can observe real streaming behavior. The endpoint will use FastAPI's `StreamingResponse` with `media_type="text/event-stream"`.

## Error Handling

FastAPI and Pydantic will validate malformed JSON and missing or empty `message` values. Validation errors will return standard FastAPI 422 responses.

## Testing and Verification

Manual verification:

```bash
uv sync
uv run uvicorn app.main:app --reload
curl -N -X POST http://127.0.0.1:8000/chat \
  -H 'content-type: application/json' \
  -d '{"message":"hello"}'
```

Expected result: multiple `data: {...}` events stream progressively, followed by a final chunk with `"finish_reason":"stop"` and then `data: [DONE]`.

Automated tests may be added if the project grows, but the initial scaffold can be verified with the curl command above.
