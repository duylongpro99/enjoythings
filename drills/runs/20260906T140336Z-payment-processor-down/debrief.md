# Debrief: payment-processor-down

Run: `drills/runs/20260906T140336Z-payment-processor-down`  
Evaluation cycles: 1  
Hints consumed: 0

## What the fault was

proc.stop payment-processor

## Reference solution

**Fault:** the payment-processor container was stopped. Sagas advance to
`PAYMENT_PROCESSING`, publish `payment.execute`, and wait for a
`payment.completed` or `payment.failed` event that no consumer produces. The
gateway keeps returning 202 because intake is healthy; only settlement is
broken.

**First useful signal:** `loadgen_payment_settle_seconds{outcome="timeout"}`
starts counting while `loadgen_requests_total{outcome="accepted"}` is flat, and
`up{job="go-services", instance="payment-processor:8080"}` is 0.

**Reference mitigation:** restart the consumer:

```sh
cd services && docker compose start payment-processor
```

The Kafka consumer group resumes from its committed offset, so the backlog of
`payment.execute` records drains in order. Payment attempts are idempotent by
payment id, so a record that was mid-flight at the stop is retried safely, not
charged twice.

**The trade-off to articulate:** intake during the drain. Two defensible
positions:

- *Keep accepting.* Customers see delay, not rejection. The backlog is bounded
  by how long the consumer was down and drains at the processor's throughput.
  Cost: the set of "debited but not settled" customers keeps growing until the
  drain catches up.
- *Pause intake until drained.* Return 503 from the gateway (or rate-limit to
  zero) so no new customer enters the stuck state. Cost: visible outage,
  and the pause has to be lifted by hand.

For an outage this short the first is usually right. The rubric grades that
the engineer named both and chose, not which they chose.

## What the engineer proposed

### Proposal 1

Stuck payments are all sitting in PAYMENT_PROCESSING and the payment-processor
scrape target is the only one reporting down, so the consumer that executes
payments is not running. Restart it: `docker compose start payment-processor`.

Keep accepting transfers while the backlog drains. The consumer resumes from its
committed Kafka offset, and payment attempts are idempotent by payment id, so
the records that were in flight when it stopped will be retried, not charged
twice. Pausing intake would turn a delay into a visible outage for a backlog
that should clear in under a minute at this traffic level.

Watch `loadgen_inflight_payments` fall back toward its steady-state value and
confirm no payment reaches FAILED as a result of the restart.

## Timeline

```yaml
timeline:
  - {at: 2026-09-06T14:27:54Z, from: NONE, to: NONE, note: "fault injected "}
  - {at: 2026-09-06T14:28:12Z, from: NONE, to: NONE, note: "probe break: pass (attempt 1) "}
  - {at: 2026-09-06T14:28:12Z, from: NONE, to: BRIEFED, note: "engineer paged "}
  - {at: 2026-09-06T14:28:48Z, from: BRIEFED, to: INVESTIGATING, note: "engineer started investigating "}
  - {at: 2026-09-06T14:45:29Z, from: INVESTIGATING, to: PROPOSED, note: "proposal 1 recorded"}
  - {at: 2026-09-06T14:45:29Z, from: PROPOSED, to: EXECUTING, note: "executor: docker compose start payment-processor, intake left open"}
  - {at: 2026-09-06T14:47:06Z, from: EXECUTING, to: EXECUTING, note: "probe fix: pass (attempt 1)"}
  - {at: 2026-09-06T14:47:06Z, from: EXECUTING, to: EVALUATED, note: "fix probe passed under load"}
  - {at: 2026-09-06T14:47:31Z, from: EVALUATED, to: DEBRIEFED, note: "resolved"}
```

## Rubric

The Executor grades the run against the following. Fill in each dimension.

| Dimension | What a strong run looks like here |
| --- | --- |
| Detection | Read `loadgen_payment_settle_seconds{outcome="timeout"}` or the Saga Health state distribution before touching logs. Weak: started from `docker compose logs` on a guess. |
| Localisation | Brief to "payment-processor is down" via `up` in Prometheus or the PAYMENT_PROCESSING pile-up, in under two probes. |
| Hypothesis quality | Wrong turns are cheap to test (a health endpoint, one PromQL query). Weak: restarting things to see what happens. |
| Fix correctness | Fix probe passes under load after the proposal is applied as written. |
| Blast radius | The proposal says what happens to the backlog when the consumer returns. Does it drain in order? Can a payment be charged twice? (It cannot: attempts are idempotent by payment id. Did the engineer know that, or assume it?) |
| Trade-off articulation | Explicit choice on intake: keep accepting transfers while the backlog drains, or pause intake at the gateway until it does. Either is defensible; asserting one without naming the other is not. |
| Hints consumed | L1: zero or one expected. Tier 3 consumed means the localisation dimension is capped. |
