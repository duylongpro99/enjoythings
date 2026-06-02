"use client";

import { useChat } from "@ai-sdk/react";
import { DefaultChatTransport } from "ai";
import { FormEvent, useMemo, useState } from "react";

function bubbleClass(role: string): string {
  return role === "user" ? "bubble bubble-user" : "bubble bubble-assistant";
}

export function ChatBox() {
  const [input, setInput] = useState("");
  const { messages, sendMessage, status, error } = useChat({
    transport: new DefaultChatTransport({
      api: "/api/chat",
    }),
  });

  const canSend = useMemo(
    () => input.trim().length > 0 && status !== "submitted" && status !== "streaming",
    [input, status],
  );

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const text = input.trim();
    if (!text || !canSend) {
      return;
    }

    setInput("");
    sendMessage({ text });
  };

  return (
    <div className="chatbox">
      <div className="messages" role="log" aria-live="polite">
        {messages.length === 0 ? (
          <p className="hint">Ask anything to start streaming.</p>
        ) : (
          messages.map((message) => (
            <article key={message.id} className={bubbleClass(message.role)}>
              <p className="role">{message.role}</p>
              <div>
                {message.parts.map((part, index) => {
                  if (part.type === "text") {
                    return (
                      <p key={`${message.id}-${index}`} className="part-text">
                        {part.text}
                      </p>
                    );
                  }

                  if (process.env.NODE_ENV === "development") {
                    return (
                      <p key={`${message.id}-${index}`} className="part-unknown">
                        Unsupported part: {part.type}
                      </p>
                    );
                  }

                  return null;
                })}
              </div>
            </article>
          ))
        )}
      </div>
      <form className="composer" onSubmit={onSubmit}>
        <input
          value={input}
          onChange={(event) => setInput(event.target.value)}
          placeholder="Type a message..."
          disabled={status === "submitted" || status === "streaming"}
          aria-label="Message"
        />
        <button type="submit" disabled={!canSend}>
          {status === "submitted" || status === "streaming" ? "Streaming..." : "Send"}
        </button>
      </form>
      {error ? <p className="error">Request failed. Check FastAPI and try again.</p> : null}
    </div>
  );
}
