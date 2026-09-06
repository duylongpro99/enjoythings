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
