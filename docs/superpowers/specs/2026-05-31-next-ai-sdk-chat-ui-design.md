# Next AI SDK Chat UI Design

Date: 2026-05-31

## Goal

Add a minimal Next.js web app that sends user text to the existing FastAPI streaming chat server and renders the assistant response progressively in the browser.

The first UI phase should stay intentionally small:

- A developer can run the existing FastAPI server.
- A developer can run a new Next.js app from `web/`.
- The browser submits chat messages to a same-origin Next route, not directly to FastAPI.
- The Next route calls the FastAPI `/chat` endpoint server-side.
- The UI streams assistant text as it arrives.
- The chat rendering path uses AI SDK `UIMessage.parts` from the start, so later structured UI parts can be added without replacing the chat surface.

## Current Project Context

The workspace already contains a minimal FastAPI server:

- `app/main.py` exposes `POST /chat`.
- The endpoint validates a non-empty `message` field.
- It returns OpenAI-style Server-Sent Events with chat completion chunk payloads.
- Tests in `tests/test_chat_stream.py` verify validation and streaming shape.

The workspace is not currently a git repository, so the design document can be written locally but cannot be committed unless git is initialized or the project is moved into a git worktree.

## Architecture

The frontend will be added as a separate Next.js app under `web/`. The FastAPI app remains in `app/`.

Runtime pieces:

- FastAPI backend: `app/main.py`
- Next server-rendered page: `web/app/page.tsx`
- Next server-side chat proxy: `web/app/api/chat/route.ts`
- Minimal client chat component: `web/components/chat-box.tsx`

Next.js should prioritize server-side behavior:

- `page.tsx` remains a Server Component.
- `/api/chat` is a Route Handler and is the only code that talks to FastAPI.
- The chatbox is the only Client Component because user input, submit state, abort state, and incremental message rendering require browser state.

The browser will call `/api/chat`. The route handler will call the FastAPI URL configured by `FASTAPI_CHAT_URL`, defaulting to `http://127.0.0.1:8000/chat`.

This keeps CORS, backend hostnames, and stream-protocol translation out of the browser.

## Package Policy

All JavaScript packages should be installed from the package manager's current `latest` dist-tag at implementation time.

Expected initial dependencies:

- `next@latest`
- `react@latest`
- `react-dom@latest`
- `ai@latest`
- `@ai-sdk/react@latest`

The implementation should prefer `create-next-app@latest` for scaffolding the Next app, then add the AI SDK packages with `@latest`. The lockfile produced during implementation is the source of truth for the exact installed versions.

## AI SDK Usage

Use `useChat` from `@ai-sdk/react` for the UI.

The chat component will use:

- `messages` for rendering the conversation.
- `sendMessage({ text })` for submission.
- `status` to disable sending while submitted or streaming.
- `error` for a small inline error message.
- `stop` only if a minimal cancel button is cheap to include; otherwise omit it in phase one.

The hook should use the default `/api/chat` endpoint or explicitly configure `DefaultChatTransport` with `api: "/api/chat"`.

Do not use `useCompletion`, because this is a multi-message chat interface.

Do not use AI SDK RSC `streamUI` in this phase. The backend is FastAPI, and the desired long-term path is typed data parts rendered by Next, not server-streamed React components from Python.

## Stream Protocol

FastAPI currently emits OpenAI-style SSE chat completion chunks. The Next route handler will bridge that backend stream to the AI SDK UI message stream expected by `useChat`.

For phase one, the route handler should:

- Accept the request shape sent by `useChat`.
- Extract the latest user text message.
- Send `{ "message": "<latest user text>" }` to FastAPI.
- Read FastAPI's SSE response.
- Extract `choices[0].delta.content` text chunks.
- Emit an AI SDK UI message stream response to the browser.

The response from `/api/chat` should include the AI SDK UI message stream header required for custom backends:

```text
x-vercel-ai-ui-message-stream: v1
```

The emitted stream should use the UI message stream start/text-delta/end pattern so the client receives `UIMessage.parts` updates.

## Future Structured UI Parts

Later complex responses should be represented as typed data parts, not as raw React components from FastAPI.

Example future part types:

- `data-card`
- `data-chart`
- `data-action-list`
- `data-feature-preview`
- `source`

FastAPI can eventually emit structured backend events. The Next proxy will translate those events into AI SDK UI message parts. The React UI will map known part types to safe local components.

The phase-one chat renderer must iterate through `message.parts` and handle at least:

- `text`
- Unknown part fallback

Unknown parts should not crash the UI. They can be ignored in production and rendered as a compact unsupported-part notice in development.

## UI

The web UI should be minimal:

- Centered page container.
- Message list with user and assistant rows.
- Text input.
- Send button.
- Disabled send state while the chat is submitted or streaming.
- Small inline error message when `useChat` reports an error.

Out of scope for this phase:

- Authentication.
- Chat persistence.
- Conversation list.
- Model picker.
- Markdown renderer.
- File uploads.
- Tool execution UI.
- Rich custom data part components.
- Production deployment configuration.

## Configuration

Add `web/.env.local.example`:

```bash
FASTAPI_CHAT_URL=http://127.0.0.1:8000/chat
```

The route handler should use this environment variable and fall back to the same local URL when it is absent.

## Development Flow

Run FastAPI:

```bash
uv sync
uv run uvicorn app.main:app --reload
```

Run Next.js from `web/`:

```bash
npm install
npm run dev
```

Then open the Next dev URL and submit a chat message.

## Error Handling

The route handler should return clear HTTP errors when:

- The incoming request does not contain a usable user text message.
- FastAPI is unavailable.
- FastAPI returns a non-2xx response.
- FastAPI returns malformed SSE data.

The UI should show a concise error message and keep the input usable after failure.

## Testing and Verification

Keep the existing FastAPI tests.

Frontend verification should include:

- Next TypeScript check or production build, depending on the scaffolded scripts.
- Manual browser test that confirms tokens appear progressively.
- Manual FastAPI-down test that confirms the UI reports an error instead of hanging.

Acceptance criteria:

- `POST /chat` FastAPI tests still pass.
- Next app starts from `web/`.
- Submitting text in the browser calls same-origin `/api/chat`.
- `/api/chat` calls FastAPI server-side.
- Assistant text streams into the UI.
- The renderer is based on `message.parts`, preserving the path to future structured UI parts.
