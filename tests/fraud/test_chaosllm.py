"""Unit tests for the drills chaos LLM server (app/fraud/chaosllm)."""

import asyncio
import json
import random

import pytest

from app.fraud.chaosllm.server import (
    ChaosConfig,
    ChaosLLMApp,
    HttpError,
    Ok,
    Truncate,
)

_PATH = "/chaos/v1/chat/completions"


async def _call(app: ChaosLLMApp, path: str = _PATH):
    """Drive the ASGI app for one POST and collect (status, headers, body)."""
    scope = {"type": "http", "method": "POST", "path": path, "headers": []}
    incoming = [{"type": "http.request", "body": b"{}", "more_body": False}]

    async def receive():
        return incoming.pop(0)

    events: list[dict] = []

    async def send(event):
        events.append(event)

    await app(scope, receive, send)
    start = next(e for e in events if e["type"] == "http.response.start")
    body = b"".join(e.get("body", b"") for e in events if e["type"] == "http.response.body")
    return start["status"], dict(start["headers"]), body


def _config(profile: str, **env) -> ChaosConfig:
    return ChaosConfig.from_env({"CHAOS_LLM_PROFILE": profile, **env})


def test_profiles_configure_knobs():
    assert _config("healthy").latency_ms == 0
    slow = _config("slow")
    assert slow.latency_ms > 0 and slow.error_rate == 0.0
    assert _config("errors").error_rate == 1.0
    assert _config("truncate").truncate is True
    flaky = _config("flaky")
    assert 0.0 < flaky.error_rate < 1.0


def test_env_overrides_win_over_profile():
    cfg = _config("healthy", CHAOS_LLM_LATENCY_MS="250", CHAOS_LLM_TRUNCATE="true")
    assert cfg.latency_ms == 250
    assert cfg.truncate is True


def test_unknown_profile_rejected():
    with pytest.raises(ValueError):
        _config("nonsense")


def test_error_rate_is_clamped():
    assert _config("healthy", CHAOS_LLM_ERROR_RATE="5").error_rate == 1.0
    assert _config("healthy", CHAOS_LLM_ERROR_RATE="-1").error_rate == 0.0


def test_decide_outcomes_per_profile():
    rng = random.Random(0)
    assert isinstance(_config("errors").decide(rng), HttpError)
    assert isinstance(_config("healthy").decide(rng), Ok)
    assert isinstance(_config("truncate").decide(rng), Truncate)


def test_healthy_response_is_openai_sse_the_driver_can_read():
    status, headers, body = asyncio.run(_call(ChaosLLMApp(_config("healthy"))))
    assert status == 200
    assert headers[b"content-type"] == b"text/event-stream"
    text = body.decode()
    assert "data: [DONE]" in text
    # The first data line parses to a delta whose content is the configured reply.
    first = text.splitlines()[0].removeprefix("data:").strip()
    content = json.loads(first)["choices"][0]["delta"]["content"]
    assert content == _config("healthy").response


def test_errors_profile_returns_500():
    status, _headers, _body = asyncio.run(_call(ChaosLLMApp(_config("errors"))))
    assert status == 500


def test_truncate_profile_omits_done():
    status, headers, body = asyncio.run(_call(ChaosLLMApp(_config("truncate"))))
    assert status == 200
    assert headers[b"content-type"] == b"text/event-stream"
    assert b"[DONE]" not in body


def test_unknown_path_is_404():
    status, _headers, _body = asyncio.run(_call(ChaosLLMApp(_config("healthy")), path="/nope"))
    assert status == 404
