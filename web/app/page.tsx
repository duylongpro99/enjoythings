import { ChatBox } from "@/components/chat-box";

export default function Home() {
  return (
    <main className="page-shell">
      <section className="chat-panel">
        <header className="chat-header">
          <h1>Stream Chat</h1>
          <p>Next.js AI SDK UI with FastAPI SSE backend.</p>
        </header>
        <ChatBox />
      </section>
    </main>
  );
}
