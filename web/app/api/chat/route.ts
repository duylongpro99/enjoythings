import { createUIMessageStream, createUIMessageStreamResponse } from "ai";

export const runtime = "nodejs";

type FastApiChunk = {
  choices?: Array<{
    delta?: {
      content?: string;
    };
    finish_reason?: string | null;
  }>;
};

function extractLatestUserText(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }

  const messages = (payload as { messages?: unknown }).messages;
  if (!Array.isArray(messages)) {
    return null;
  }

  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const message = messages[i];
    if (!message || typeof message !== "object") {
      continue;
    }

    const role = (message as { role?: unknown }).role;
    if (role !== "user") {
      continue;
    }

    const parts = (message as { parts?: unknown }).parts;
    if (!Array.isArray(parts)) {
      continue;
    }

    for (const part of parts) {
      if (!part || typeof part !== "object") {
        continue;
      }

      if ((part as { type?: unknown }).type === "text") {
        const text = (part as { text?: unknown }).text;
        if (typeof text === "string" && text.trim().length > 0) {
          return text;
        }
      }
    }
  }

  return null;
}

function sseEvents(stream: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  return (async function* generate() {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });

      let boundary = buffer.indexOf("\n\n");
      while (boundary !== -1) {
        const rawEvent = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);

        const dataLines = rawEvent
          .split("\n")
          .map((line) => line.trim())
          .filter((line) => line.startsWith("data:"))
          .map((line) => line.slice(5).trim());

        if (dataLines.length > 0) {
          yield dataLines.join("\n");
        }

        boundary = buffer.indexOf("\n\n");
      }
    }

    if (buffer.trim().length > 0) {
      const dataLines = buffer
        .split("\n")
        .map((line) => line.trim())
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trim());

      if (dataLines.length > 0) {
        yield dataLines.join("\n");
      }
    }
  })();
}

export async function POST(req: Request): Promise<Response> {
  let payload: unknown;
  try {
    payload = await req.json();
  } catch {
    return Response.json({ error: "Invalid JSON body" }, { status: 400 });
  }

  const userText = extractLatestUserText(payload);
  if (!userText) {
    return Response.json({ error: "Missing user text message" }, { status: 400 });
  }

  const fastApiUrl = process.env.FASTAPI_CHAT_URL ?? "http://127.0.0.1:8000/chat";
  let fastApiResponse: Response;

  try {
    fastApiResponse = await fetch(fastApiUrl, {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify({ message: userText }),
      signal: req.signal,
    });
  } catch {
    return Response.json({ error: "Failed to reach FastAPI backend" }, { status: 502 });
  }

  if (!fastApiResponse.ok || !fastApiResponse.body) {
    return Response.json(
      { error: `FastAPI responded with ${fastApiResponse.status}` },
      { status: 502 },
    );
  }
  const fastApiBody = fastApiResponse.body;

  const stream = createUIMessageStream({
    execute: async ({ writer }) => {
      const blockId = crypto.randomUUID();

      writer.write({
        type: "text-start",
        id: blockId,
      });

      for await (const data of sseEvents(fastApiBody)) {
        if (data === "[DONE]") {
          break;
        }

        try {
          const parsed = JSON.parse(data) as FastApiChunk;
          const content = parsed.choices?.[0]?.delta?.content;
          if (typeof content === "string" && content.length > 0) {
            writer.write({
              type: "text-delta",
              id: blockId,
              delta: content,
            });
          }
        } catch {
          throw new Error("Malformed SSE chunk from FastAPI");
        }
      }

      writer.write({
        type: "text-end",
        id: blockId,
      });
    },
    onError: () => "Streaming failed",
  });

  return createUIMessageStreamResponse({
    stream,
    headers: {
      "x-vercel-ai-ui-message-stream": "v1",
    },
  });
}
