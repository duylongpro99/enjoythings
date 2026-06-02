from dataclasses import dataclass


@dataclass(frozen=True)
class ChatMessage:
    role: str
    content: str


@dataclass(frozen=True)
class ChatRequest:
    messages: list[ChatMessage]


@dataclass(frozen=True)
class ChatDelta:
    content: str

