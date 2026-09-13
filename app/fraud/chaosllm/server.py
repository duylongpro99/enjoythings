"""Env-driven chaos LLM server.

Serves ``POST .../chat/completions`` in the OpenAI streaming shape the fraud
worker's ``OpenAICompatibleDriver`` consumes (``data: {json}\\n\\n`` deltas
terminated by ``data: [DONE]``), but degrades according to a profile so a drill
can make the fraud worker's model calls slow, error, or return a broken stream.

The degradation *decision* is a pure function (:func:`ChaosConfig.decide`) so it
is unit-testable without a running server; only the sleep and socket I/O live in
the ASGI app.
"""

from __future__ import annotations

import asyncio
import json
import os
import random
from collections.abc import Mapping
from dataclasses import dataclass

# Profile -> (latency_ms, error_rate, truncate). Explicit env knobs override
# whichever field they name; the profile supplies the rest.
_PROFILES: Mapping[str, tuple[int, float, bool]] = {
    "healthy": (0, 0.0, False),
    "slow": (10_000, 0.0, False),
    "errors": (0, 1.0, False),
    "truncate": (0, 0.0, True),
    "flaky": (500, 0.5, False),
}

_DEFAULT_RESPONSE = '{"risk_score": 0.1, "action": "allow", "reason_code": "chaos_ok"}'


class ChaosOutcome:
    """Marker base for what the server should do for one request."""


@dataclass(frozen=True)
class Ok(ChaosOutcome):
    content: str


@dataclass(frozen=True)
class HttpError(ChaosOutcome):
    status: int = 500


@dataclass(frozen=True)
class Truncate(ChaosOutcome):
    content: str


@dataclass(frozen=True)
class ChaosConfig:
    profile: str
    latency_ms: int
    error_rate: float
    truncate: bool
    port: int
    response: str

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> ChaosConfig:
        env = os.environ if env is None else env
        profile = env.get("CHAOS_LLM_PROFILE", "healthy").strip().lower()
        if profile not in _PROFILES:
            raise ValueError(
                f"unknown CHAOS_LLM_PROFILE {profile!r}; "
                f"expected one of {', '.join(sorted(_PROFILES))}"
            )
        latency_ms, error_rate, truncate = _PROFILES[profile]
        if "CHAOS_LLM_LATENCY_MS" in env:
            latency_ms = int(env["CHAOS_LLM_LATENCY_MS"])
        if "CHAOS_LLM_ERROR_RATE" in env:
            error_rate = float(env["CHAOS_LLM_ERROR_RATE"])
        if "CHAOS_LLM_TRUNCATE" in env:
            truncate = env["CHAOS_LLM_TRUNCATE"].strip().lower() in ("1", "true", "yes", "on")
        return cls(
            profile=profile,
            latency_ms=latency_ms,
            error_rate=max(0.0, min(1.0, error_rate)),
            truncate=truncate,
            port=int(env.get("CHAOS_LLM_PORT", "18091")),
            response=env.get("CHAOS_LLM_RESPONSE", _DEFAULT_RESPONSE),
        )

    def decide(self, rng: random.Random) -> ChaosOutcome:
        """Pick the outcome for one request. Latency is applied by the caller."""
        if self.error_rate >= 1.0 or (self.error_rate > 0.0 and rng.random() < self.error_rate):
            return HttpError()
        if self.truncate:
            return Truncate(self.response)
        return Ok(self.response)


class ChaosLLMApp:
    """ASGI app routing any ``*/chat/completions`` POST to a chaos outcome."""

    def __init__(self, config: ChaosConfig, *, rng: random.Random | None = None) -> None:
        self._config = config
        self._rng = rng or random.Random()

    async def __call__(self, scope, receive, send) -> None:
        if scope["type"] != "http":
            return
        await _drain(receive)
        if not scope.get("path", "").endswith("/chat/completions"):
            await _send(send, 404, b"unknown endpoint")
            return

        if self._config.latency_ms > 0:
            await asyncio.sleep(self._config.latency_ms / 1000.0)

        outcome = self._config.decide(self._rng)
        if isinstance(outcome, HttpError):
            await _send(send, outcome.status, b"chaos: injected error")
            return
        if isinstance(outcome, Truncate):
            # A partial, unterminated stream: one broken data line, no [DONE].
            body = b'data: {"choices":[{"delta":{"content":"' + outcome.content.encode()[:8]
            await _send(send, 200, body, content_type=b"text/event-stream")
            return
        await _send(send, 200, _sse(outcome.content), content_type=b"text/event-stream")


def _sse(content: str) -> bytes:
    chunk = json.dumps({"choices": [{"delta": {"content": content}}]}, separators=(",", ":"))
    return f"data: {chunk}\n\ndata: [DONE]\n\n".encode()


async def _drain(receive) -> None:
    while True:
        message = await receive()
        if not message.get("more_body", False):
            return


async def _send(send, status: int, body: bytes, content_type: bytes = b"text/plain") -> None:
    await send(
        {
            "type": "http.response.start",
            "status": status,
            "headers": [(b"content-type", content_type)],
        }
    )
    await send({"type": "http.response.body", "body": body})


def main() -> None:
    import uvicorn

    config = ChaosConfig.from_env()
    uvicorn.run(
        ChaosLLMApp(config),
        host="0.0.0.0",  # noqa: S104 — a drill-only container on the stack network
        port=config.port,
        log_level="warning",
        lifespan="off",
    )


if __name__ == "__main__":
    main()
