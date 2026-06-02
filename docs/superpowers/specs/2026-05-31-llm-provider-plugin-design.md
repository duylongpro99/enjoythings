# LLM Provider Plugin Design

Date: 2026-05-31

## Goal

Add an LLM provider layer to the FastAPI chat backend so the server can reply to user messages through any OpenAI-compatible endpoint without tying the core system to a specific provider SDK or provider dependency.

The first implementation will support provider selection at server startup through environment configuration. Clients must not be able to select or override the provider. The server will call one configured default provider, retry timeout failures up to 3 total attempts, and return a clear timeout error if all attempts fail.

## Current Context

The project currently has:

- `app/main.py`, a FastAPI app exposing `POST /chat`.
- `POST /chat`, which accepts a non-empty `message` and streams OpenAI-style SSE chat completion chunks.
- `web/app/api/chat/route.ts`, a Next.js route that calls the FastAPI `/chat` endpoint and converts the backend SSE stream into Vercel AI SDK UI message stream events.
- `tests/test_chat_stream.py`, which verifies the existing backend SSE contract.

The current backend response is deterministic mock text. The provider layer will replace that mock generation while preserving the existing HTTP and SSE contract used by the frontend.

## Architecture

The server will use a plugin-style provider architecture with a stable application interface:

- `ChatService`
  - Accepts the validated user message.
  - Builds a generic chat request.
  - Calls the configured default provider through `LLMPort`.
  - Owns retry behavior for timeout failures.

- `LLMPort`
  - Defines the provider-agnostic streaming interface.
  - Exposes a method such as `stream_chat(request) -> AsyncIterator[ChatDelta]`.
  - Is the only provider dependency used by the application layer.

- `DriverRegistry`
  - Reads provider configuration from environment variables at startup.
  - Validates provider IDs, default provider selection, and supported driver types.
  - Instantiates the selected default provider driver.

- `Driver plugins`
  - Implement `LLMPort`.
  - Convert generic chat requests into provider-specific HTTP requests.
  - Convert provider streaming responses into generic text deltas.
  - Keep protocol-specific code isolated from the core server.

- `SSEPresenter`
  - Converts generic text deltas into the existing OpenAI-style SSE chunks.
  - Preserves the current `data: {...}\n\n` and `data: [DONE]\n\n` response contract.

The first driver plugin will be `openai_compatible`. It will use plain HTTP streaming rather than a provider SDK. This keeps server dependencies stable and avoids coupling the backend to OpenAI, Ollama, vLLM, LM Studio, or any other specific implementation.

## Configuration

Provider definitions will be supplied at startup through `LLM_PROVIDERS_JSON`. The default provider will be selected with `LLM_DEFAULT_PROVIDER`.

Example:

```json
{
  "providers": [
    {
      "id": "local",
      "driver_type": "openai_compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "api_key_env": "LOCAL_LLM_API_KEY",
      "model": "llama3.1",
      "timeout_seconds": 30
    },
    {
      "id": "openai",
      "driver_type": "openai_compatible",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "gpt-4.1-mini",
      "timeout_seconds": 30
    }
  ]
}
```

Rules:

- `LLM_PROVIDERS_JSON` must be valid JSON.
- Each provider must have a unique `id`.
- `LLM_DEFAULT_PROVIDER` must match one configured provider ID.
- `driver_type` must be supported by the registry.
- `base_url` must point to the provider's OpenAI-compatible API base URL, without requiring callers to know endpoint details.
- `api_key_env` names the environment variable that contains the secret. Secrets must not be embedded directly in `LLM_PROVIDERS_JSON`.
- `model` is required and is passed to the provider request.
- `timeout_seconds` controls the HTTP timeout for one attempt.

Changing the provider means changing environment values and restarting the server. Runtime provider switching is intentionally out of scope.

## Request Flow

`POST /chat` will keep the same public request body:

```json
{
  "message": "hello"
}
```

Internal flow:

1. FastAPI validates the request body.
2. The endpoint calls `ChatService.stream_reply(message)`.
3. `ChatService` sends a generic chat request to the default `LLMPort`.
4. The `openai_compatible` driver sends a request to `{base_url}/chat/completions`.
5. The request includes `stream: true`, the configured model, and the user message.
6. The driver parses provider SSE chunks and yields generic text deltas.
7. The SSE presenter emits the existing OpenAI-style chat completion SSE chunks.

OpenAI-compatible provider request shape:

```json
{
  "model": "configured-model",
  "messages": [
    {
      "role": "user",
      "content": "hello"
    }
  ],
  "stream": true
}
```

The frontend route does not need to change because the backend endpoint and stream format remain stable.

## Error Handling

Retry behavior:

- Retry timeout failures only.
- Use 3 total attempts.
- Do not fall back to another provider.
- If all attempts timeout, return a clear timeout error.
- Non-timeout provider failures return immediately as provider errors.

Configuration errors must fail during application startup or dependency construction before `/chat` handles requests. Request-time provider failures must produce stable HTTP errors from `/chat` instead of leaking raw provider responses or secrets.

Expected request-time error categories:

- Timeout after 3 total attempts.
- Provider connection failure.
- Provider returns non-2xx status.
- Provider returns malformed stream data.

The stream must not emit a successful `[DONE]` marker after a provider failure. If a failure happens before response streaming starts, `/chat` must return a normal error response. If a failure happens after streaming starts, the server must stop streaming and let the HTTP stream fail.

## Testing

Tests must avoid real network calls to OpenAI, Ollama, vLLM, LM Studio, or any other provider.

Coverage:

- Config parsing validates malformed JSON, missing fields, duplicate provider IDs, unsupported `driver_type`, missing default provider, and missing API key env variables.
- Registry tests verify the default provider resolves to the expected driver.
- Driver tests use a mocked HTTP transport or fake stream that emits OpenAI-compatible SSE chunks and verifies text deltas are parsed correctly.
- Retry tests verify timeout failures are attempted 3 total times and then return a timeout error.
- Retry tests verify non-timeout provider errors do not retry.
- API tests preserve the existing `/chat` SSE contract so the Next.js route remains compatible.

The current mock response behavior can remain available only as a test fixture or explicit fake driver, not as the default production provider.

## Out Of Scope

- Client-controlled provider selection.
- Runtime provider changes without server restart.
- Automatic fallback to another provider.
- Provider-specific SDKs.
- Non-OpenAI-compatible protocols.
- Tool calling, multimodal inputs, or conversation memory.
