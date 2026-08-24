import { afterEach, describe, expect, it, vi } from "vitest";

import { POST } from "./route";

function chatRequest(body: unknown): Request {
  return new Request("http://localhost/api/chat", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

function userMessage(text: string) {
  return {
    messages: [
      {
        id: "1",
        role: "user",
        parts: [{ type: "text", text }],
      },
    ],
  };
}

function sseResponse(chunks: string[]): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const encoder = new TextEncoder();
      for (const chunk of chunks) {
        controller.enqueue(encoder.encode(chunk));
      }
      controller.close();
    },
  });
  return new Response(stream, { status: 200 });
}

async function readAll(response: Response): Promise<string> {
  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let out = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    out += decoder.decode(value, { stream: true });
  }
  return out;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("POST /api/chat", () => {
  it("rejects a body that is not JSON", async () => {
    const response = await POST(chatRequest("not json"));

    expect(response.status).toBe(400);
    await expect(response.json()).resolves.toEqual({ error: "Invalid JSON body" });
  });

  it("rejects a conversation with no user text", async () => {
    const response = await POST(
      chatRequest({ messages: [{ id: "1", role: "assistant", parts: [{ type: "text", text: "hi" }] }] }),
    );

    expect(response.status).toBe(400);
    await expect(response.json()).resolves.toEqual({ error: "Missing user text message" });
  });

  it("sends only the latest user text to the backend", async () => {
    const fetchMock = vi.fn().mockResolvedValue(sseResponse(["data: [DONE]\n\n"]));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubEnv("FASTAPI_CHAT_URL", "http://backend.test/chat");

    await POST(
      chatRequest({
        messages: [
          { id: "1", role: "user", parts: [{ type: "text", text: "first" }] },
          { id: "2", role: "assistant", parts: [{ type: "text", text: "reply" }] },
          { id: "3", role: "user", parts: [{ type: "text", text: "second" }] },
        ],
      }),
    );

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://backend.test/chat");
    expect(JSON.parse(init.body as string)).toEqual({ message: "second" });
  });

  it("reports an unreachable backend as a bad gateway", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("connection refused")));

    const response = await POST(chatRequest(userMessage("hello")));

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "Failed to reach FastAPI backend" });
  });

  it("reports a backend error status as a bad gateway", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("boom", { status: 500 })));

    const response = await POST(chatRequest(userMessage("hello")));

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "FastAPI responded with 500" });
  });

  it("streams backend deltas as UI message chunks", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        sseResponse([
          'data: {"choices":[{"delta":{"content":"Hel"}}]}\n\n',
          'data: {"choices":[{"delta":{"content":"lo"}}]}\n\n',
          "data: [DONE]\n\n",
        ]),
      ),
    );

    const response = await POST(chatRequest(userMessage("hi")));
    const body = await readAll(response);

    expect(response.headers.get("x-vercel-ai-ui-message-stream")).toBe("v1");
    expect(body).toContain('"type":"text-start"');
    expect(body).toContain('"delta":"Hel"');
    expect(body).toContain('"delta":"lo"');
    expect(body).toContain('"type":"text-end"');
  });

  it("surfaces a malformed backend chunk as a stream error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(sseResponse(["data: {not json}\n\n", "data: [DONE]\n\n"])),
    );

    const response = await POST(chatRequest(userMessage("hi")));
    const body = await readAll(response);

    expect(body).toContain("Streaming failed");
  });
});
