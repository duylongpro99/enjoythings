# Drill load generator

Constant-rate transfer traffic for the enjoythings target. Without it most
scenarios are invisible: an idle stack has no latency to climb and no error
rate to move.

- **Source:** `services/cmd/loadgen` and `services/internal/loadgen`. It lives
  in the services module because it reuses `services/devtools/smoke` and the
  services Dockerfile (`SERVICE=loadgen`).
- **Overlay:** `docker-compose.loadgen.yml` here. `drills/targets/enjoythings/load`
  applies it.
- **Metrics** on `:8080/metrics`, scraped by the stack's Prometheus as job `loadgen`:

| Series | Labels | Meaning |
| --- | --- | --- |
| `loadgen_requests_total` | `outcome` = accepted, rejected, error | Gateway answer to `POST /v1/transfers` |
| `loadgen_request_duration_seconds` | | Gateway round trip |
| `loadgen_payment_settle_seconds` | `outcome` = completed, failed, timeout | Accepted transfer to terminal saga state |
| `loadgen_inflight_payments` | | Accepted, not yet terminal |

Settlement latency is the primary detection path for anything downstream of
the gateway: a stopped consumer leaves the gateway returning 202 while
`loadgen_payment_settle_seconds{outcome="timeout"}` starts counting.

## Profiles

| Profile | Rate | Users | Amount |
| --- | --- | --- | --- |
| `steady` | 5/s | 20 | 1250 cents |

Only `steady` exists in this slice; bursts and diurnal shapes are deferred
until a scenario needs them (spec §13 Q3).

## Staying inside the gateway's rate limit

The gateway limits each user to a burst of `RATE_LIMIT_BURST` and then one
request per `RATE_LIMIT_REFILL_EVERY` (default 1/s). The generator models many
customers each inside that allowance: `LOADGEN_USER_BUDGET_RPS` (default 1)
is the per-user budget, transfers take what they need, and status polls spend
the remainder through a client-side token bucket. A growing backlog therefore
slows polling rather than producing a 429 storm that an engineer would read as
a symptom. The generator refuses to start if `rate / users` alone exceeds the
budget; add users. Change the budget together with the gateway's refill.

## Funding

Each user is verified through the gateway and given a wallet pair. The source
wallet is funded by writing the platform database directly, as the smoke
suites do, because no product endpoint deposits money. The seed lasts far
longer than a drill at any supported profile.
