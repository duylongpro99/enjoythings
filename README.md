# FastAPI + Next Stream Chat

Minimal full-stack chat example:

- FastAPI backend streams OpenAI-style chat completion SSE chunks.
- Next.js frontend uses Vercel AI SDK `useChat` and streams responses in the UI.

## Backend

Install and run the FastAPI server:

```bash
uv sync
uv run uvicorn app.main:app --reload
```

The backend endpoint is `POST /chat`.

Required backend environment can be exported in your shell:

```bash
export LLM_PROVIDERS_JSON='{"providers":[{"id":"local","driver_type":"openai_compatible","base_url":"http://127.0.0.1:11434/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"llama3.1","timeout_seconds":30}]}'
export LLM_DEFAULT_PROVIDER='local'
export LOCAL_LLM_API_KEY='replace-me'
```

Or copied from `.env.example` into a backend `.env` file:

```bash
cp .env.example .env
```

```dotenv
LLM_PROVIDERS_JSON='{"providers":[{"id":"local","driver_type":"openai_compatible","base_url":"http://127.0.0.1:11434/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"llama3.1","timeout_seconds":30}]}'
LLM_DEFAULT_PROVIDER=local
LOCAL_LLM_API_KEY=replace-me
```

Shell environment variables take precedence over `.env` values.

## Frontend

In a separate terminal:

```bash
cd web
pnpm install
pnpm dev
```

Optional environment config:

```bash
cp .env.local.example .env.local
```

Default backend URL is `http://127.0.0.1:8000/chat`.
