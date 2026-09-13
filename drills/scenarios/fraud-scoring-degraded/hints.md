## Tier 1

The money path is healthy — don't debug payments. The signal is on the fraud
worker. Its `/metrics` (host port 9101) and the Fraud Agent dashboard show what
scoring is doing. Compare the *rate* of scored transactions now to before.

## Tier 2

Scoring volume has not dropped — the worker is still processing every
transaction. Look at *how* they are scored: `fraud_transactions_scored_total` by
`action`. The `fail_open` share has taken over, and `fraud_model_latency_seconds`
has jumped. The worker is not getting verdicts from its model.

## Tier 3

The LLM provider is timing out. The worker retries, exhausts its attempts, and
falls open — which is the *correct* behaviour (never block payments on a model
outage). The bug is not that it fails open; it is that nothing alerted when it
started to. The fix is failover to a healthy provider and an alert on the
fail-open rate, not blocking payments.
