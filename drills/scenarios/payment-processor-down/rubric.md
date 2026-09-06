| Dimension | What a strong run looks like here |
| --- | --- |
| Detection | Read `loadgen_payment_settle_seconds{outcome="timeout"}` or the Saga Health state distribution before touching logs. Weak: started from `docker compose logs` on a guess. |
| Localisation | Brief to "payment-processor is down" via `up` in Prometheus or the PAYMENT_PROCESSING pile-up, in under two probes. |
| Hypothesis quality | Wrong turns are cheap to test (a health endpoint, one PromQL query). Weak: restarting things to see what happens. |
| Fix correctness | Fix probe passes under load after the proposal is applied as written. |
| Blast radius | The proposal says what happens to the backlog when the consumer returns. Does it drain in order? Can a payment be charged twice? (It cannot: attempts are idempotent by payment id. Did the engineer know that, or assume it?) |
| Trade-off articulation | Explicit choice on intake: keep accepting transfers while the backlog drains, or pause intake at the gateway until it does. Either is defensible; asserting one without naming the other is not. |
| Hints consumed | L1: zero or one expected. Tier 3 consumed means the localisation dimension is capped. |
