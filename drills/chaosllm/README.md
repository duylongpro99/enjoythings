# Chaos LLM overlay — dependency faults

This overlay adds a scripted, misbehaving OpenAI-compatible endpoint so the
drills adapter can inject `dep.replace llm-endpoint <profile>` (spec §5, §9).
The fraud worker is the only consumer of `LLM_PROVIDERS_JSON`, so this primitive
is inherently fraud-scoped.

The server is `app/fraud/chaosllm/server.py`, built into the fraud image and run
as the `chaos-llm` console script. It speaks the exact SSE shape the worker's
`OpenAICompatibleDriver` consumes (`data: {json}` deltas, `data: [DONE]`), then
degrades per its profile.

## Profiles

| Profile | Behaviour | Worker effect |
| --- | --- | --- |
| `healthy` | Normal scripted reply | control; scoring works |
| `slow` | Sleeps past the provider timeout | `ProviderTimeoutError` → 3 retries → **fail-open** |
| `errors` | Returns HTTP 500 | `ProviderHTTPStatusError` → fail-open |
| `truncate` | Partial, unterminated stream | malformed-stream error → fail-open |
| `flaky` | ~50% errors, mild latency | intermittent fail-open |

Knobs `CHAOS_LLM_LATENCY_MS`, `CHAOS_LLM_ERROR_RATE`, `CHAOS_LLM_TRUNCATE`, and
`CHAOS_LLM_RESPONSE` override the profile defaults.

## How `dep.replace` wires it

1. `CHAOS_LLM_PROFILE=<profile>` and bring `chaos-llm` up in the base project.
2. `env.set fraud-worker LLM_PROVIDERS_JSON=…` (a one-provider registry pointing
   at `http://chaos-llm:18091/chaos/v1`, `timeout_seconds: 5`) and
   `LLM_DEFAULT_PROVIDER=chaos`, recreating the worker.

`revert` stops the chaos server (`dep.restore`) and restores the real registry
(`env.unset fraud-worker`), recreating the worker against its real provider. The
symptom is silent — fail-open is the *correct* behaviour — so a drill detects it
through `fraud_transactions_scored_total{action="fail_open"}`, not a saga state
(see `services/devtools/drillmetric`).
