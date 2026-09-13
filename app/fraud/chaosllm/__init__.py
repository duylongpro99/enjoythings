"""Chaos LLM endpoint for drills.

A standalone, env-driven OpenAI-compatible SSE server that misbehaves on demand
(latency, HTTP errors, truncated streams) so the drills adapter can inject
``dep.replace llm-endpoint <profile>`` against the fraud worker (spec §5, §9).
"""
